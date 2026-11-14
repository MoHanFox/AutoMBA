package mba

import "strings"

// Definition describes a source-level variable or memory reference.
type Definition struct {
	Name       string `json:"name"`
	Source     string `json:"source"`
	Simplified string `json:"simplified"`
	Reason     string `json:"reason"`
	Line       int    `json:"line"`
}

// Step is a coarse before/after record for one source statement.
type Step struct {
	Line   int    `json:"line"`
	Before string `json:"before"`
	After  string `json:"after"`
	Reason string `json:"reason"`
}

type analyzedStmt struct {
	name  string
	root  string
	op    string
	rhs   string
	line  int
	mem   bool
	valid bool
}

func analyzeStmts(code string, width uint) []analyzedStmt {
	chunks := splitStatements(code)
	var out []analyzedStmt
	offset := 0
	for _, chunk := range chunks {
		line := strings.Count(code[:minInt(offset, len(code))], "\n") + 1
		offset += len(chunk)
		ps, ok := parsePostStmt(chunk)
		if !ok || ps.rhs == nil {
			continue
		}
		a := analyzedStmt{op: ps.op, rhs: ps.rhs.Format(width), line: line, valid: true}
		if ps.simple {
			a.name = ps.name
		} else if ps.mem {
			a.root = rootIdent(ps.prefix)
			a.mem = true
			a.name = "*" + a.root
		} else {
			a.valid = false
		}
		if a.valid {
			out = append(out, a)
		}
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// AnalyzeDefinitions builds variable definitions and a coarse transformation
// trace from the original and simplified code.
func AnalyzeDefinitions(input, output string, width uint) ([]Definition, []Step) {
	in := analyzeStmts(input, width)
	out := analyzeStmts(output, width)
	outByKey := map[string]analyzedStmt{}
	for _, s := range out {
		key := s.name
		if s.mem {
			key = s.name + s.op
		}
		outByKey[key] = s
	}
	defsByName := map[string]Definition{}
	var steps []Step
	for _, s := range in {
		key := s.name
		if s.mem {
			key = s.name + s.op
		}
		o, found := outByKey[key]
		reason := "kept"
		after := s.rhs
		if !found {
			reason = "inlined, merged, or removed"
			after = "(removed)"
		} else if o.rhs != s.rhs {
			reason = "MBA / arithmetic simplification"
			after = o.rhs
		} else if s.mem {
			reason = "kept memory update"
		}
		defsByName[s.name] = Definition{
			Name: s.name, Source: s.rhs, Simplified: after,
			Reason: reason, Line: s.line,
		}
		steps = append(steps, Step{Line: s.line, Before: s.rhs, After: after, Reason: reason})
	}
	for _, s := range out {
		if s.mem {
			if _, ok := defsByName[s.name]; !ok {
				defsByName[s.name] = Definition{Name: s.name, Source: s.rhs, Simplified: s.rhs, Reason: "kept memory update", Line: s.line}
			}
			continue
		}
		if _, ok := defsByName[s.name]; !ok {
			defsByName[s.name] = Definition{
				Name: s.name, Source: "(generated)", Simplified: s.rhs,
				Reason: "CSE / reference merge temporary", Line: s.line,
			}
		}
	}
	var defs []Definition
	for _, d := range defsByName {
		defs = append(defs, d)
	}
	sortDefinitions(defs)
	if len(steps) > 300 {
		steps = steps[:300]
	}
	return defs, steps
}

func sortDefinitions(defs []Definition) {
	for i := 1; i < len(defs); i++ {
		for j := i; j > 0 && defs[j].Line < defs[j-1].Line; j-- {
			defs[j], defs[j-1] = defs[j-1], defs[j]
		}
	}
}
