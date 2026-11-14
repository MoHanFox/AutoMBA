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
}

func runSimplification(code string, width int, mode string, keepRefs string, verify bool, preserve bool) (out apiResult) {
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

	var res mba.ProcessResult
	switch mode {
	case "refs":
		res = mba.ProcessRefs(code, uint(width), keepRefs, preserve)
	case "deep":
		res = mba.ProcessDeep(code, uint(width))
	case "iterate":
		res = mba.Process(code, uint(width), true)
	default:
		res = mba.Process(code, uint(width), false)
	}
	out.Output = res.Output
	out.Error = res.Error
	out.Changed = res.Changed
	out.Statements = res.Statements
	out.Simplified = res.Simplified
	out.Merged = res.Merged
	out.Passes = res.Passes
	out.Warnings = res.Warnings
	if verify && out.Error == "" {
		v := mba.VerifyCode(code, out.Output, uint(width))
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
		res := runSimplification(string(data), *width, mode, *keep, *verify, *preserve)
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
