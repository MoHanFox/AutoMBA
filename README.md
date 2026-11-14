# AutoMBA

用 Go + WebView 实现的桌面版 **MBA（Mixed Boolean-Arithmetic，混合布尔算术）化简器**。左侧粘贴 C 代码，右侧得到化简后的只读结果；算法参考 GAMBA 的“线性 MBA + 位级分解”思路，但针对反编译代码片段做了 C 语法适配。

![界面预览](docs/screenshot.png)

## 功能

- **双栏深色 IDE 界面**：左侧 C/MBA 输入，右侧只读结果（可选中、可复制）。
- **CLion / Darcula 风格代码高亮**：C 关键字、类型、数字、字符串、注释、函数名分色显示，输入与结果同步高亮。
- **简化**：自动识别赋值、`+=`、`return` 和裸表达式中的 MBA，并逐条化简。
- **以结果再简化**：把右侧结果作为输入，传播直线代码中纯标量变量的定义后再化简，常能跨多条语句折叠。
- **引用保留**：输入 `a1;a2;a3` 后，只保留这些根引用对应的语句，其余局部临时变量做数据流内联合并。对示例代码会直接压成 `*a1` / `*a3` / `*a2` 三条语句。
- **复制结果**：一键写入剪贴板。
- **位宽切换**：8 / 16 / 32 / 64 位；所有常量、掩码与算术都按所选位宽取模。

## 构建与运行

环境要求：

- Go 1.24+（本项目在 Go 1.27 下开发）
- CGO 可用的 C 编译器。Windows 推荐 MinGW-w64 的 `gcc`
- Windows 10/11 的 **WebView2 Runtime**（多数系统已自带）
- 首次构建需要能访问 Go module 源；`build.bat` 默认使用 `goproxy.cn`

```bat
build.bat
```

会生成：

- `AutoMBA.exe`：GUI 程序
- `automba-cli.exe`：命令行版本，便于脚本化验证

也可以直接：

```powershell
go build -o AutoMBA.exe .
go build -o automba-cli.exe .
```

### 命令行

```powershell
Get-Content examples\sample.c -Raw | .\automba-cli.exe -cli -w 32
Get-Content examples\sample.c -Raw | .\automba-cli.exe -cli -w 32 -deep
Get-Content examples\sample.c -Raw | .\automba-cli.exe -cli -w 32 -keep "a1;a2;a3"
```

参数：

- `-cli`：从标准输入读取，输出去到标准输出
- `-w 8|16|32|64`：位宽，默认 32
- `-deep`：启用跨语句的纯标量定义传播
- `-keep "a1;a2;a3"`：只保留这些根引用，其余局部变量内联合并

## 输入格式

支持反编译代码中常见的 C 表达式子集：

- 运算符：`+ - * / % & | ^ ~ ! << >>`、比较、`&& ||`、三元表达式
- 赋值：`=`、`+=`、`-=`、`*=`, `/=`, `%=`, `&=`, `|=`, `^=`, `<<=`, `>>=`
- 类型转换：`(unsigned int)`、`(_DWORD *)`、`(__int64)` 等
- 下标、成员访问、解引用、取地址、函数调用
- 多行表达式、注释、函数体中的语句

函数调用、数组读取、解引用等**副作用未知或不可建模式**的部分会被当作原子（opaque atom）参与化简；工具只做表达式改写，不执行代码。普通化简模式会保留这些调用/读取，引用合并模式可能把它们内联到使用点（见下节注意事项）。

## 引用保留 / 变量合并

在“引用保留”输入框中填写需要保留的**根引用名**，用分号分隔，例如：

```text
a1;a2;a3
```

点击“简化”后，工具会：

1. 按顺序把局部标量赋值（`v5 = ...`）记录为符号定义；
2. 后续表达式引用这些变量时，递归代入其定义；
3. 删除不再需要的局部变量声明/赋值；
4. 只输出根引用命中列表的语句，例如 `*a1 += ...`、`*a3 += ...`、`*a2 += ...`；
5. 对代入后的完整表达式重新做 MBA 化简。

对题目示例输入 `a1;a2;a3`，输出会压缩为：

```c
*a1 += sub_140006120((unsigned int)*a2) ^ *(_DWORD *)&aYcsvllo3xM9zAa[4 * sub_140006140(*a3, (unsigned int)i)] + *a3;
*a3 += 0x7F4A7C15;
*a2 += *a3 + *(_DWORD *)&aYcsvllo3xM9zAa[4 * (unsigned int)sub_140006160(*a3, i)] ^ sub_140006120((unsigned int)*a1);
```

注意：变量合并按**数据流**处理，函数调用和内存读取会被内联到使用点；如果这些操作有副作用，展开后的求值顺序/次数可能与原始程序不同，因此结果更适合做代数分析，不保证逐字节等价于原程序执行。带 `if` / `for` / `while` / `switch` / `goto` 的代码会自动退回逐条化简。

## 化简器思路

实现分为几层，全部在 `internal/mba` 中：

1. **词法 / 语法分析**：手写 C 表达式 Pratt parser，生成 AST。无法解析的语句原样保留并给出警告。
2. **结构化化简**：常量折叠、双重否定、`x & x`/`x | x`/`x ^ x`、吸收律、同类项合并、`x + x = 2*x` 等。
3. **位级 ANF（代数正规形）**：
   - 把纯位运算表达式按位做 Möbius 变换，得到精确的 XOR-of-AND 形式；
   - 任意常量掩码都保留，不会出现“把 0x0F 当成 1”的经典错误；
   - 对变量顺序做分解搜索，构造更短的 XOR/OR 形式。
4. **Bit-slice 合成**：
   - 对每个比特位置求出它关于各原子的布尔真值表；
   - 按真值表分组，使用预计算的最优 1~3 变量布尔公式表合成每组；
   - 再与 ANF、原始结构比较成本，选最短且经过随机验证的候选。
5. **算术层**：展开加减法、按常量倍率合并同类项，处理 `x - x = 0`、`x + x = 2*x` 等。
6. **深化简**：在直线代码中记录纯标量定义（无调用、无下标、无解引用），代入后续表达式再做上述化简。遇到 `if/for/while/switch/goto` 会自动退化为逐条化简。
7. **引用合并**：按 `a1;a2;a3` 保留根引用，把其余局部标量定义递归代入并删除；最后只输出命中保留列表的语句。

每个位运算候选在返回前都会用固定的伪随机输入做一致性验证；`internal/mba/verify_test.go` 还会对示例代码的每条语句做随机等价性检查。

## 示例

输入（`examples/sample.c`）中的部分片段：

```c
v9 = ~(v7 ^ (v15 ^ 0x79D0A2C0) & ~v15) | v7 & ~((v15 ^ 0x79D0A2C0) & ~v15);
v10 = ~(~(v5 & 0x862F5D3F | v6) | v5 & 0x862F5D3F ^ 0xC6AB228 ^ v6 ^ 0xC6AB228) | v5 & 0x862F5D3F ^ v6;
v14 = ~(~v12 | ~v11) | v12 ^ 0xBC734574 ^ v11 ^ 0xBC734574;
*a2 += ~v14 & v13 | ~v13 & v14;
```

32 位“简化”结果：

```c
v9  = (v7 | v15) & 0x79D0A2C0 | 0x862F5D3F;
v10 = v6 | v5 & 0x862F5D3F;
v14 = v12 | v11;
*a2 += v14 ^ v13;
```

再点“以结果再简化”后，示例会进一步折叠为：

```c
v9  = -1;
v10 = v5 ^ 0x79D0A2C0;
v13 = v15 ^ 0x35486230;
```

## 注意事项

- **位宽是语义的一部分**：对 32 位反编译代码请选 32。选 64 位时 `~` 和常量按 64 位解释，结果会与 32 位不同，这是预期行为。
- 深化简只对**直线代码**做跨语句代入；带控制流的代码会自动回退到逐条化简。
- 输入只用于化简，不会执行；但请不要把工具当作反编译器或完整 C 前端，它只关心表达式层的 MBA。
- WebView2 的用户数据目录默认放在可执行文件旁的 `.webview`；如果目录不可写，会退回临时目录。该目录已加入 `.gitignore`。

## 目录

```
main.go                 WebView 入口、Go/JS 绑定、CLI
web/index.html          深色双栏界面（内嵌）
internal/mba/
  expr.go               AST / 格式化 / 求值
  lexer.go, parser.go   C 表达式词法与语法
  simplify.go           结构化与算术化简
  bitwise.go            ANF / bit-slice / 候选验证
  bitslice.go           最优 1~3 变量布尔公式表
  deep.go               跨语句定义传播
  refs.go               引用保留 / 局部变量内联合并
  process.go            语句切分与代码级处理
examples/sample.c       示例
```

## 参考

算法受以下工作启发，代码为独立实现：

- Benjamin Reichenwallner, Peter Meerwald-Stadler. *Simplification of General Mixed Boolean-Arithmetic Expressions: GAMBA.* WoRMA 2023. [arXiv:2305.06763](https://arxiv.org/abs/2305.06763)
- [DenuvoSoftwareSolutions/GAMBA](https://github.com/DenuvoSoftwareSolutions/GAMBA)（GPLv3，仅参考算法与流程，未复制其代码）

WebView 绑定使用 [webview/webview_go](https://github.com/webview/webview_go)（MIT）。
