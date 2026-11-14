# AutoMBA

A desktop MBA (Mixed Boolean-Arithmetic) deobfuscator for decompiler-style C snippets, written in Go with a WebView2 UI.

Paste C code or an MBA expression on the left; the simplified result appears on the right. The engine is inspired by GAMBA-style linear MBA decomposition and bit-level simplification, but is adapted to real decompiler output instead of single expressions.

![AutoMBA settings](docs/screenshot.png)

## Features

- **Dark two-pane IDE UI** with CLion/Darcula syntax highlighting.
- **MBA simplification**: arithmetic identities, term merging, exact bit-level ANF, bit-slice truth tables and optimal Boolean formulas for small atom sets.
- **Simplification modes**: normal, simplify-again and dataflow-aware deep mode.
- **Reference-preserving merge**: enter roots such as `a1;a2;a3` to inline local scalar definitions and keep only statements that write those roots.
- **Preserve side effects**: keep call/load temporaries instead of aggressively inlining them.
- **Non-pure call annotations**: semicolon-separated function names that must never be inlined, for example `sub_140006120;sub_140006140`.
- **Auto simplify**: debounce input and simplify automatically.
- **Dead code elimination**: remove pure scalar assignments whose values are never used later.
- **Cross-statement CSE**: materialize repeated pure subexpressions that are stable between their first and last use.
- **Signed arithmetic**: explicit signed semantics for arithmetic right shift, negative division/modulo and comparisons.
- **Extended C expressions**: ternary `?:`, comparison/logical operators, casts, member access, indexing and pointer arithmetic.
- **Sampled equivalence check**: deterministic differential execution of the original and simplified programs.
- **Diff view, warnings panel and simplification steps**: inspect line changes, warnings and per-statement before/after reasons.
- **Click a variable** in the input or result to see where it came from and how it changed.
- **Persistent settings** in `config.json` next to the executable.

## Requirements

- Windows 10/11 with WebView2 Runtime.
- Go 1.24 or newer.
- MinGW-w64 GCC for CGO builds (tested with GCC 16.2.0).
- A WebView2 profile directory next to the executable (`.webview`); the app sets `WEBVIEW2_USER_DATA_FOLDER` automatically.

## Build

```powershell
.\build.bat
```

This produces:

- `AutoMBA.exe` — GUI build (`-H=windowsgui`).
- `automba-cli.exe` — console/CLI build.

Manual build:

```powershell
go build -trimpath -ldflags "-s -w -H=windowsgui" -o AutoMBA.exe .
go build -trimpath -ldflags "-s -w" -o automba-cli.exe .
```

If the Go module proxy is unreachable, use:

```powershell
$env:GOPROXY = "https://goproxy.cn,direct"
```

## Usage

### GUI

1. Paste decompiled C code or an MBA expression into **C / MBA Input**.
2. Click **Simplify** (or **Simplify Again** to iterate).
3. Optionally enter reference roots in **Keep refs**, for example `a1;a2;a3`.
4. Click the gear to configure width, language and analysis options.

The result header contains three tools:

- **Steps** — per-statement before/after transformation trace with a reason.
- **Diff** — side-by-side original/simplified line diff.
- **Warnings** — verification status and simplification warnings, shown as an inline overlay over the result panel.

Variables are clickable in both editors. The popover shows the source expression, the simplified form, the source line and the reason.

### CLI

```powershell
Get-Content examples\sample.c -Raw | .\automba-cli.exe -cli -w 32 -keep "a1;a2;a3"
```

Flags:

| Flag | Meaning |
| --- | --- |
| `-cli` | Read C from stdin, print the result and exit. |
| `-w 32` | Bit width: `8`, `16`, `32` or `64`. |
| `-deep` | Use dataflow-aware deep simplification. |
| `-keep "a1;a2;a3"` | Enable reference-preserving merge for these roots. |
| `-verify` | Run the sampled equivalence check. |
| `-preserve` | Keep call/load temporaries to preserve side effects. |
| `-dead` | Remove dead pure scalar assignments. |
| `-cse` | Eliminate repeated pure subexpressions. |
| `-extended` | Simplify ternary/comparison/logical expressions (default `true`). |
| `-signed` | Use signed `>>`, `/`, `%` and comparisons (default `true`). |
| `-impure "sub_140006120;sub_140006140"` | Calls that must never be inlined. |

Example:

```powershell
Get-Content examples\sample.c -Raw |
  .\automba-cli.exe -cli -w 32 -keep "a1;a2;a3" -preserve -verify -signed
```

## Settings and `config.json`

Settings are stored in `config.json` next to `AutoMBA.exe`:

```json
{
  "width": 32,
  "language": "en",
  "verify": false,
  "preserveSideEffects": false,
  "autoSimplify": false,
  "deadCode": false,
  "extendedC": true,
  "signed": true,
  "cse": false,
  "impureCalls": ""
}
```

- `width`: `8`, `16`, `32` or `64`, default `32`.
- `language`: `en` (English, default) or `zh` (Simplified Chinese).
- `verify`: run the sampled equivalence check after simplification.
- `preserveSideEffects`: keep call/memory-read temporaries during reference merge.
- `autoSimplify`: simplify automatically after typing stops.
- `deadCode`: remove unused pure scalar assignments.
- `extendedC`: simplify ternary/comparison/logical expressions.
- `signed`: signed semantics for right shift, division, modulo and comparisons.
- `cse`: cross-statement common subexpression elimination.
- `impureCalls`: semicolon-separated call names that must never be inlined.

## Semantics and limitations

### Signed arithmetic

Bitwise operations and `+`, `-`, `*` are width-agnostic two's-complement operations. Signed mode additionally affects:

- `>>`: arithmetic (sign-propagating) right shift.
- `/` and `%`: C-style truncation toward zero for negative operands.
- `<`, `<=`, `>`, `>=`: signed comparisons.

Turning signed mode off uses unsigned semantics for those operators.

### Extended C expressions

The parser supports ternary `?:`, comparison and logical operators, casts, member access, array indexing and pointer arithmetic. Unsupported or opaque operations are preserved as atoms rather than dropped. When **Extended C expressions** is off, ternary/comparison/logical operators are kept conservative and are not folded.

### Reference merge and side effects

Reference merge is dataflow-oriented. By default, calls and memory reads may be inlined or duplicated. This is useful for algebraic analysis but does not guarantee the original evaluation order or call count.

Enable **Preserve side effects** to keep call/load results in temporaries. Calls listed in **Non-pure calls** are never inlined, even in aggressive mode.

### CSE and dead code

- **Dead code elimination** only removes pure scalar assignments that are not used later. Calls and memory reads are retained because they may have side effects.
- **CSE** only considers pure subexpressions that occur at least twice and are stable (their input variables are not assigned between first and last use). It inserts width-appropriate temporaries such as `int __cse1` or `unsigned int __cse1`.

### Equivalence check

The equivalence check executes the original and simplified programs over 256 deterministic samples. Calls and memory loads are modeled as deterministic external values. It compares final memory roots (or common scalar variables) and is a strong sanity check, **not a formal proof**. Use an SMT-based checker if a proof is required.

## Example

Input excerpt:

```c
v18 = sub_140006120((unsigned int)*a2);
v17 = *a3;
v4 = sub_140006140(*a3, (unsigned int)i);
*a1 += v18 ^ ...;
*a3 += 0x7F4A7C15;
```

With `a1;a2;a3` and side-effect preservation:

```c
v18 = sub_140006120((unsigned int)*a2);
v17 = *a3;
v4 = sub_140006140(*a3, (unsigned int)i);
*a1 += v18 ^ *(_DWORD *)&aYcsvllo3xM9zAa[4 * v4] + v17;
*a3 += 0x7F4A7C15;
v15 = sub_140006120((unsigned int)*a1);
v16 = *a3;
v5 = v16 + *(_DWORD *)&aYcsvllo3xM9zAa[4 * (unsigned int)sub_140006160(*a3, i)];
*a2 += v5 ^ v15;
```

## Project layout

```text
main.go                 WebView entry, bindings, CLI
config.go               config.json persistence
internal/mba/           parser, simplifier, passes and verifier
  lexer.go              C lexer
  parser.go             Pratt expression parser
  simplify.go           arithmetic/bitwise simplifier
  bitwise.go            ANF and bit-slice candidates
  bitslice.go           optimal Boolean tables
  refs.go               reference merge and purity handling
  deep.go               cross-statement propagation
  process.go            statement-level processing
  postpass.go           DCE and CSE
  verify_code.go        sampled differential verifier
  analysis.go           definitions and step trace
web/index.html          UI, i18n and syntax highlighting
docs/                   screenshots
examples/               sample input
```

## Tests

```powershell
go test ./...
```

## License

[MIT](LICENSE) © 2026 MoHanFox
