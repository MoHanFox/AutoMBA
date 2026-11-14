package main

import (
	"embed"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	webview "github.com/webview/webview_go"

	"automba/internal/mba"
)

//go:embed web/index.html
var webFS embed.FS

type apiResult struct {
	Output       string            `json:"output"`
	Error        string            `json:"error,omitempty"`
	Changed      bool              `json:"changed"`
	Statements   int               `json:"statements"`
	Simplified   int               `json:"simplified"`
	Merged       int               `json:"merged"`
	Passes       int               `json:"passes"`
	DurationMS   int64             `json:"durationMs"`
	Warnings     []string          `json:"warnings,omitempty"`
	InputBytes   int               `json:"inputBytes"`
	OutputBytes  int               `json:"outputBytes"`
	Verification *mba.Verification `json:"verification,omitempty"`
	Definitions  []mba.Definition  `json:"definitions,omitempty"`
	Steps        []mba.Step        `json:"steps,omitempty"`
}

type simplifyOptions struct {
	Verify              bool   `json:"verify"`
	PreserveSideEffects bool   `json:"preserveSideEffects"`
	AutoSimplify        bool   `json:"autoSimplify"`
	DeadCode            bool   `json:"deadCode"`
	ExtendedC           bool   `json:"extendedC"`
	Signed              bool   `json:"signed"`
	CSE                 bool   `json:"cse"`
	ImpureCalls         string `json:"impureCalls"`
}

func (o simplifyOptions) mbaOptions() mba.Options {
	var impure []string
	for _, f := range strings.FieldsFunc(o.ImpureCalls, func(r rune) bool {
		return r == ';' || r == ',' || r == '\n' || r == ' ' || r == '\t'
	}) {
		if f = strings.TrimSpace(f); f != "" {
			impure = append(impure, f)
		}
	}
	return mba.Options{
		DeadCode: o.DeadCode, ExtendedC: o.ExtendedC, Signed: o.Signed,
		CSE: o.CSE, ImpureCalls: impure,
	}
}

func runSimplification(code string, width int, mode string, keepRefs string, opt simplifyOptions) (out apiResult) {
	start := time.Now()
	defer func() {
		if r := recover(); r != nil {
			out = apiResult{Error: fmt.Sprintf("化简器内部错误: %v", r)}
		}
		out.DurationMS = time.Since(start).Milliseconds()
		if out.Output == "" && out.Error == "" {
			out.Error = "化简没有返回结果"
		}
		out.InputBytes = len(code)
		out.OutputBytes = len(out.Output)
	}()

	mopt := opt.mbaOptions()
	var res mba.ProcessResult
	switch mode {
	case "refs":
		res = mba.ProcessRefsWithOptions(code, uint(width), keepRefs, opt.PreserveSideEffects, mopt)
	case "deep":
		res = mba.ProcessDeepWithOptions(code, uint(width), mopt)
	case "iterate":
		res = mba.ProcessWithOptions(code, uint(width), true, mopt)
	default:
		res = mba.ProcessWithOptions(code, uint(width), false, mopt)
	}
	out.Output = res.Output
	out.Error = res.Error
	out.Changed = res.Changed
	out.Statements = res.Statements
	out.Simplified = res.Simplified
	out.Merged = res.Merged
	out.Passes = res.Passes
	out.Warnings = res.Warnings
	if out.Error == "" {
		defs, steps := mba.AnalyzeDefinitions(code, out.Output, uint(width))
		out.Definitions = defs
		out.Steps = steps
	}
	if opt.Verify && out.Error == "" {
		v := mba.VerifyCodeWithOptions(code, out.Output, uint(width), mopt)
		out.Verification = &v
	}
	return out
}

// prepareWebViewDataDir keeps WebView2's profile next to the executable. This
// matters for portable use and for restricted/low-integrity workspaces where the
// default %APPDATA% profile is not writable by the WebView2 child processes.
func prepareWebViewDataDir() {
	if runtime.GOOS != "windows" {
		return
	}
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Join(filepath.Dir(exe), ".webview")
		if err := os.MkdirAll(dir, 0o700); err == nil {
			if probe, err := os.CreateTemp(dir, ".probe-*"); err == nil {
				name := probe.Name()
				_ = probe.Close()
				_ = os.Remove(name)
				_ = os.Setenv("WEBVIEW2_USER_DATA_FOLDER", dir)
				return
			}
		}
	}
	// Fall back to the normal temp directory.
	dir := filepath.Join(os.TempDir(), "AutoMBA-webview")
	if err := os.MkdirAll(dir, 0o700); err == nil {
		_ = os.Setenv("WEBVIEW2_USER_DATA_FOLDER", dir)
	}
}

func main() {
	cli := flag.Bool("cli", false, "read C code from stdin, print simplified code, and exit")
	width := flag.Int("w", 32, "bit width: 8, 16, 32 or 64")
	deep := flag.Bool("deep", false, "use dataflow-aware deep simplification in CLI mode")
	keep := flag.String("keep", "", "reference roots to keep, e.g. a1;a2;a3 (enables merge mode)")
	verify := flag.Bool("verify", false, "run sampled equivalence verification")
	preserve := flag.Bool("preserve", false, "keep call/load temporaries to preserve side effects")
	dead := flag.Bool("dead", false, "remove dead pure scalar assignments")
	cse := flag.Bool("cse", false, "eliminate repeated pure subexpressions")
	extended := flag.Bool("extended", true, "simplify ternary/comparison/logical expressions")
	signed := flag.Bool("signed", true, "use signed arithmetic for >>, /, % and comparisons")
	impure := flag.String("impure", "", "comma-separated non-pure function names")
	flag.Parse()

	if *cli {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "读取标准输入失败:", err)
			os.Exit(1)
		}
		mode := "normal"
		if *deep {
			mode = "deep"
		}
		if strings.TrimSpace(*keep) != "" {
			mode = "refs"
		}
		opt := simplifyOptions{
			Verify: *verify, PreserveSideEffects: *preserve,
			DeadCode: *dead, CSE: *cse, ExtendedC: *extended, Signed: *signed,
			ImpureCalls: *impure,
		}
		res := runSimplification(string(data), *width, mode, *keep, opt)
		if res.Error != "" {
			fmt.Fprintln(os.Stderr, res.Error)
			os.Exit(1)
		}
		fmt.Print(res.Output)
		if !strings.HasSuffix(res.Output, "\n") {
			fmt.Println()
		}
		for _, w := range res.Warnings {
			fmt.Fprintln(os.Stderr, "警告:", w)
		}
		if res.Verification != nil {
			fmt.Fprintf(os.Stderr, "验证: %s (%d cases) %s\n", res.Verification.Status, res.Verification.Cases, res.Verification.Message)
		}
		return
	}

	html, err := webFS.ReadFile("web/index.html")
	if err != nil {
		panic(err)
	}
	prepareWebViewDataDir()
	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("AutoMBA")
	w.SetSize(1000, 700, webview.HintNone)
	if err := w.Bind("simplifyCode", runSimplification); err != nil {
		panic(err)
	}
	if err := w.Bind("loadConfig", loadAppConfig); err != nil {
		panic(err)
	}
	if err := w.Bind("saveConfig", saveAppConfig); err != nil {
		panic(err)
	}
	w.SetHtml(string(html))
	w.Run()
}
