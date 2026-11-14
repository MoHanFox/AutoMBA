package mba

import (
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// VerifyZ3WithOptions proves bit-vector equivalence for straight-line code,
// if/else branches and member cells by exporting SMT-LIB and invoking Z3. It is
// deliberately conservative: unsupported constructs return "skipped".
func VerifyZ3WithOptions(original, simplified string, width uint, opt Options) Verification {
	if width != 8 && width != 16 && width != 32 && width != 64 {
		width = 32
	}
	z3, z3Err := findZ3()
	py, pyArgs, pyErr := findPythonZ3()
	if z3Err != nil && pyErr != nil {
		return Verification{Status: "skipped", Message: "Z3 not found (PATH/Z3_EXE or Python z3-solver)"}
	}
	po, err := parseZ3Program(original)
	if err != nil {
		return Verification{Status: "skipped", Message: "Z3 parse: " + err.Error()}
	}
	ps, err := parseZ3Program(simplified)
	if err != nil {
		return Verification{Status: "skipped", Message: "Z3 parse: " + err.Error()}
	}

	b := &smtBuilder{
		width: width, signed: opt.Signed,
		funcs: map[string]string{}, cells: map[string]bool{}, varsSeen: map[string]bool{},
	}
	so := &smtState{vars: map[string]string{}, mem: map[string]string{}}
	ss := &smtState{vars: map[string]string{}, mem: map[string]string{}}
	if err := b.execZ3(so, po); err != nil {
		return Verification{Status: "skipped", Message: "Z3 encoding: " + err.Error()}
	}
	if err := b.execZ3(ss, ps); err != nil {
		return Verification{Status: "skipped", Message: "Z3 encoding: " + err.Error()}
	}

	var asserts []string
	if len(b.cells) > 0 {
		for cell := range b.cells {
			a := b.cellValue(so, cell)
			c := b.cellValue(ss, cell)
			asserts = append(asserts, fmt.Sprintf("(assert (not (= %s %s)))", a, c))
		}
	} else if len(b.varsSeen) > 0 {
		for v := range b.varsSeen {
			a := b.varValue(so, v)
			c := b.varValue(ss, v)
			asserts = append(asserts, fmt.Sprintf("(assert (not (= %s %s)))", a, c))
		}
	}
	if len(asserts) == 0 {
		return Verification{Status: "skipped", Message: "Nothing to compare"}
	}
	sort.Strings(asserts)

	var script strings.Builder
	script.WriteString("(set-logic QF_AUFBV)\n")
	b.writeDecls(&script)
	for _, a := range asserts {
		script.WriteString(a + "\n")
	}
	script.WriteString("(check-sat)\n")

	dir, err := os.MkdirTemp("", "automba-z3-*")
	if err != nil {
		// Low-integrity builds cannot always use %TEMP%; fall back next to the
		// executable, which is writable for the user running the app.
		if exe, exeErr := os.Executable(); exeErr == nil {
			dir, err = os.MkdirTemp(filepath.Dir(exe), ".z3tmp-*")
		}
	}
	if err != nil {
		return Verification{Status: "skipped", Message: "Z3 temp dir: " + err.Error()}
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "check.smt2")
	if err := os.WriteFile(path, []byte(script.String()), 0o600); err != nil {
		return Verification{Status: "skipped", Message: "Z3 temp file: " + err.Error()}
	}
	text, runErr := "", error(nil)
	if z3Err == nil {
		out, err := exec.Command(z3, "-smt2", "-T:20", path).CombinedOutput()
		text = strings.TrimSpace(string(out))
		runErr = err
	}
	if text == "" && pyErr == nil {
		pyCode := `import sys,z3; s=z3.Solver(); s.set("timeout", 20000); s.add(z3.parse_smt2_file(sys.argv[1])); print(s.check())`
		args := append(append([]string{}, pyArgs...), "-c", pyCode, path)
		out, err := exec.Command(py, args...).CombinedOutput()
		text = strings.TrimSpace(string(out))
		runErr = err
	}
	switch {
	case strings.HasPrefix(text, "unsat"):
		return Verification{Status: "passed", Cases: 1, Message: "Z3 proved equivalence"}
	case strings.HasPrefix(text, "sat"):
		return Verification{Status: "failed", Cases: 1, Message: "Z3 found a counterexample"}
	case strings.HasPrefix(text, "unknown"):
		return Verification{Status: "skipped", Message: "Z3 returned unknown (timeout or unsupported theory)"}
	default:
		if runErr != nil {
			msg := strings.TrimSpace(text)
			if msg == "" {
				msg = runErr.Error()
			}
			return Verification{Status: "skipped", Message: "Z3 failed: " + msg}
		}
		return Verification{Status: "skipped", Message: "Unexpected Z3 output: " + text}
	}
}

func findZ3() (string, error) {
	if p := strings.TrimSpace(os.Getenv("Z3_EXE")); p != "" {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	if p, err := exec.LookPath("z3"); err == nil {
		return p, nil
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, name := range []string{"z3.exe", "z3"} {
			p := filepath.Join(dir, name)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p, nil
			}
		}
	}
	return "", fmt.Errorf("z3 not found")
}

func findPythonZ3() (string, []string, error) {
	candidates := [][]string{
		{"py", "-3.15"},
		{"py", "-3.14"},
		{"py", "-3.13"},
		{"py", "-3"},
		{"python"},
		{"python3"},
	}
	for _, c := range candidates {
		args := append(append([]string{}, c[1:]...), "-c", "import z3")
		if err := exec.Command(c[0], args...).Run(); err == nil {
			return c[0], append([]string{}, c[1:]...), nil
		}
	}
	return "", nil, fmt.Errorf("python z3-solver not found")
}

type smtState struct {
	vars map[string]string
	mem  map[string]string
}

func newSMTState(b *smtBuilder, roots []string) *smtState {
	s := &smtState{vars: map[string]string{}, mem: map[string]string{}}
	for _, r := range roots {
		s.mem[r] = smtRoot(r)
	}
	return s
}

type smtBuilder struct {
	width    uint
	signed   bool
	funcs    map[string]string
	symbols  map[string]string
	order    []string
	cells    map[string]bool
	varsSeen map[string]bool
}

func (b *smtBuilder) zero() string { return fmt.Sprintf("(_ bv0 %d)", b.width) }
func (b *smtBuilder) one() string  { return fmt.Sprintf("(_ bv1 %d)", b.width) }

func smtRoot(name string) string {
	return "r_" + smtName(name)
}

func smtName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "x"
	}
	return b.String()
}

func shortHash(s string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return fmt.Sprintf("%08x", h.Sum32())
}

func (b *smtBuilder) declareUF(name string, arity int) string {
	key := fmt.Sprintf("%s/%d", name, arity)
	if existing, ok := b.funcs[key]; ok {
		return existing
	}
	var sorts strings.Builder
	for i := 0; i < arity; i++ {
		sorts.WriteString(fmt.Sprintf(" (_ BitVec %d)", b.width))
	}
	decl := fmt.Sprintf("(declare-fun %s (%s) (_ BitVec %d))", name, sorts.String(), b.width)
	b.funcs[key] = name
	b.order = append(b.order, decl)
	return name
}

func (b *smtBuilder) symbol(prefix, key string) string {
	if b.symbols == nil {
		b.symbols = map[string]string{}
	}
	idx := prefix + key
	if name, ok := b.symbols[idx]; ok {
		return name
	}
	name := prefix + smtName(key) + "_" + shortHash(key)
	decl := fmt.Sprintf("(declare-fun %s () (_ BitVec %d))", name, b.width)
	b.symbols[idx] = name
	b.order = append(b.order, decl)
	return name
}

func (b *smtBuilder) varSymbol(name string) string {
	if b.varsSeen == nil {
		b.varsSeen = map[string]bool{}
	}
	b.varsSeen[name] = true
	return b.symbol("v_", name)
}

func (b *smtBuilder) cellSymbol(cell string) string {
	if b.cells == nil {
		b.cells = map[string]bool{}
	}
	b.cells[cell] = true
	return b.symbol("m_", cell)
}

func (b *smtBuilder) varValue(s *smtState, name string) string {
	if b.varsSeen == nil {
		b.varsSeen = map[string]bool{}
	}
	b.varsSeen[name] = true
	if v, ok := s.vars[name]; ok {
		return v
	}
	return b.varSymbol(name)
}

func (b *smtBuilder) cellValue(s *smtState, cell string) string {
	if b.cells == nil {
		b.cells = map[string]bool{}
	}
	b.cells[cell] = true
	if v, ok := s.mem[cell]; ok {
		return v
	}
	return b.cellSymbol(cell)
}

func (b *smtBuilder) writeDecls(out *strings.Builder) {
	for _, d := range b.order {
		out.WriteString(d + "\n")
	}
}

func (b *smtBuilder) runProgram(s *smtState, p vProgram) error {
	for _, st := range p.stmts {
		rhs, err := b.expr(s, st.expr)
		if err != nil {
			return err
		}
		var cur string
		if st.mem {
			cur = s.mem[st.root]
		} else {
			cur = s.vars[st.root]
		}
		if cur == "" {
			cur = b.zero()
		}
		next, err := b.apply(st.op, cur, rhs)
		if err != nil {
			return err
		}
		if st.mem {
			s.mem[st.root] = next
		} else {
			s.vars[st.root] = next
		}
	}
	return nil
}

func (b *smtBuilder) apply(op, old, rhs string) (string, error) {
	switch op {
	case "=":
		return rhs, nil
	case "+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "<<=", ">>=":
		return b.binary(strings.TrimSuffix(op, "="), old, rhs)
	}
	return "", fmt.Errorf("unsupported assignment %q", op)
}

func (b *smtBuilder) binary(op, a, c string) (string, error) {
	switch op {
	case "+":
		return fmt.Sprintf("(bvadd %s %s)", a, c), nil
	case "-":
		return fmt.Sprintf("(bvsub %s %s)", a, c), nil
	case "*":
		return fmt.Sprintf("(bvmul %s %s)", a, c), nil
	case "&":
		return fmt.Sprintf("(bvand %s %s)", a, c), nil
	case "|":
		return fmt.Sprintf("(bvor %s %s)", a, c), nil
	case "^":
		return fmt.Sprintf("(bvxor %s %s)", a, c), nil
	case "<<":
		return fmt.Sprintf("(bvshl %s %s)", a, c), nil
	case ">>":
		if b.signed {
			return fmt.Sprintf("(bvashr %s %s)", a, c), nil
		}
		return fmt.Sprintf("(bvlshr %s %s)", a, c), nil
	case "/":
		if b.signed {
			return fmt.Sprintf("(bvsdiv %s %s)", a, c), nil
		}
		return fmt.Sprintf("(bvudiv %s %s)", a, c), nil
	case "%":
		if b.signed {
			return fmt.Sprintf("(bvsrem %s %s)", a, c), nil
		}
		return fmt.Sprintf("(bvurem %s %s)", a, c), nil
	case "==":
		return b.boolToBV(fmt.Sprintf("(= %s %s)", a, c)), nil
	case "!=":
		return b.boolToBV(fmt.Sprintf("(distinct %s %s)", a, c)), nil
	case "<":
		if b.signed {
			return b.boolToBV(fmt.Sprintf("(bvslt %s %s)", a, c)), nil
		}
		return b.boolToBV(fmt.Sprintf("(bvult %s %s)", a, c)), nil
	case "<=":
		if b.signed {
			return b.boolToBV(fmt.Sprintf("(bvsle %s %s)", a, c)), nil
		}
		return b.boolToBV(fmt.Sprintf("(bvule %s %s)", a, c)), nil
	case ">":
		if b.signed {
			return b.boolToBV(fmt.Sprintf("(bvsgt %s %s)", a, c)), nil
		}
		return b.boolToBV(fmt.Sprintf("(bvugt %s %s)", a, c)), nil
	case ">=":
		if b.signed {
			return b.boolToBV(fmt.Sprintf("(bvsge %s %s)", a, c)), nil
		}
		return b.boolToBV(fmt.Sprintf("(bvuge %s %s)", a, c)), nil
	case "&&":
		ba := b.nonzero(a)
		bc := b.nonzero(c)
		return b.boolToBV(fmt.Sprintf("(and %s %s)", ba, bc)), nil
	case "||":
		ba := b.nonzero(a)
		bc := b.nonzero(c)
		return b.boolToBV(fmt.Sprintf("(or %s %s)", ba, bc)), nil
	}
	return "", fmt.Errorf("unsupported operator %q", op)
}

func (b *smtBuilder) boolToBV(cond string) string {
	return fmt.Sprintf("(ite %s %s %s)", cond, b.one(), b.zero())
}

func (b *smtBuilder) nonzero(v string) string {
	return fmt.Sprintf("(not (= %s %s))", v, b.zero())
}

func (b *smtBuilder) expr(s *smtState, e *Expr) (string, error) {
	if e == nil {
		return "", fmt.Errorf("nil expression")
	}
	switch e.Kind {
	case KNum:
		return fmt.Sprintf("(_ bv%d %d)", e.Num&maskFor(b.width), b.width), nil
	case KVar:
		return b.varValue(s, e.Name), nil
	case KUnary:
		if e.Op == "*" {
			if len(e.Kids) == 1 && e.Kids[0].Kind == KVar {
				return b.cellValue(s, "root:"+e.Kids[0].Name), nil
			}
			base, idx, ok := b.loadParts(s, e.Kids[0])
			if ok {
				fn := b.declareUF("ld_"+smtName(base)+"_"+shortHash(base), 1)
				return fmt.Sprintf("(%s %s)", fn, idx), nil
			}
			addr, err := b.expr(s, e.Kids[0])
			if err != nil {
				return "", fmt.Errorf("unsupported dereference %s", e.Format(b.width))
			}
			fn := b.declareUF("ldp", 1)
			return fmt.Sprintf("(%s %s)", fn, addr), nil
		}
		if len(e.Kids) != 1 {
			return "", fmt.Errorf("unsupported unary %q", e.Op)
		}
		x, err := b.expr(s, e.Kids[0])
		if err != nil {
			return "", err
		}
		switch e.Op {
		case "~":
			return fmt.Sprintf("(bvnot %s)", x), nil
		case "-":
			return fmt.Sprintf("(bvneg %s)", x), nil
		case "+":
			return x, nil
		case "!":
			return b.boolToBV(fmt.Sprintf("(= %s %s)", x, b.zero())), nil
		}
		return "", fmt.Errorf("unsupported unary %q", e.Op)
	case KBinary:
		a, err := b.expr(s, e.Kids[0])
		if err != nil {
			return "", err
		}
		c, err := b.expr(s, e.Kids[1])
		if err != nil {
			return "", err
		}
		return b.binary(e.Op, a, c)
	case KTernary:
		cond, err := b.expr(s, e.Kids[0])
		if err != nil {
			return "", err
		}
		a, err := b.expr(s, e.Kids[1])
		if err != nil {
			return "", err
		}
		c, err := b.expr(s, e.Kids[2])
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(ite %s %s %s)", b.nonzero(cond), a, c), nil
	case KCast:
		return b.expr(s, e.Kids[0])
	case KCall:
		if len(e.Kids) == 0 {
			return "", fmt.Errorf("empty call")
		}
		callee := e.Kids[0].Format(b.width)
		var args []string
		for _, k := range e.Kids[1:] {
			v, err := b.expr(s, k)
			if err != nil {
				return "", err
			}
			args = append(args, v)
		}
		fn := b.declareUF("fn_"+smtName(callee)+"_"+shortHash(callee), len(args))
		if len(args) == 0 {
			return fmt.Sprintf("(%s)", fn), nil
		}
		return fmt.Sprintf("(%s %s)", fn, strings.Join(args, " ")), nil
	case KIndex:
		base := e.Kids[0].Format(b.width)
		idx, err := b.expr(s, e.Kids[1])
		if err != nil {
			return "", err
		}
		fn := b.declareUF("idx_"+smtName(base)+"_"+shortHash(base), 1)
		return fmt.Sprintf("(%s %s)", fn, idx), nil
	case KMember:
		return b.cellValue(s, "member:"+e.Key()), nil
	case KOpaque:
		fn := b.declareUF("op_"+smtName(e.Name)+"_"+shortHash(e.Name), 0)
		return fmt.Sprintf("(%s)", fn), nil
	}
	return "", fmt.Errorf("unsupported expression")
}

func (b *smtBuilder) loadParts(s *smtState, e *Expr) (base, idx string, ok bool) {
	if e == nil {
		return "", "", false
	}
	switch e.Kind {
	case KCast:
		return b.loadParts(s, e.Kids[0])
	case KUnary:
		if e.Op == "&" {
			return b.loadParts(s, e.Kids[0])
		}
	case KIndex:
		base = e.Kids[0].Format(b.width)
		var err error
		idx, err = b.expr(s, e.Kids[1])
		if err != nil {
			return "", "", false
		}
		return base, idx, true
	}
	return "", "", false
}
