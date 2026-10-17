# go2lean 设计文档

> 状态：草案 v0.2（2026-10-17）。从零开始的整体设计，用于在动手之前对齐目标、边界、接口和分期。
> v0.2 相对 v0.1 的主要变化：M1 改为**双前端**（`go/ssa` 与 LLVM）共用一个参考 lean-mlir 设计的 **Lean SSA 内核**，`go/ssa` 前端先行。
> 带「待验证」标记的内容是基于文档和经验的判断，第一个 spike 必须实测。

## 0. 一句话

把 Go 程序翻译成 Lean 里的可执行 SSA 语义模型，让 agent 在模型上提出性质，用 Lean 的判定工具找出**反例**，再用真实的 Go 工具链把反例跑成**可复现的 PoC**。找不到反例时，同一个模型可用于**证明**。

## 1. 目标与非目标

### 1.1 目标

1. **三模块闭环**：
   - M1 `programmatic`：确定性翻译。两条前端路线进入同一个 Lean SSA 内核：
     - **F-GoSSA**：Go → `golang.org/x/tools/go/ssa` → Lean `GoSSA` dialect（先做）。
     - **F-LLVM**：Go → TinyGo → LLVM IR → Lean `LLVM` dialect（随后；也是其他语言的入口）。
   - M2 `agentic`：LLM agent 在模型上提出性质，驱动 Lean 产生反例或证明。
   - M3 `poc`：把反例落成真实 Go 测试，在真实 Go 工具链上验证，给出判定。
2. **可信度来自 M3，不来自模型**：模型允许不精确，但每个对外报告的 bug 都必须有一个在 `go test` 下真实复现的 PoC。
3. **一个内核，多个 dialect**：Lean 侧的 SSA 内核与前端无关。dialect 只定义类型、指令和单条指令的语义；CFG 解释、fuel、havoc、panic 由内核统一提供。
4. **双路交叉**：同一个 Go 函数经两条前端得到两个模型，用同一组输入对比。不一致即翻译 bug 或 TinyGo/Go 语义差异，两者都值得报告。
5. **双向输出**：每条性质最终要么是 `confirmed` 的 PoC，要么是 `proved`/`bounded_proved` 的 Lean 定理，要么明确标记为 `unknown` 及原因。

### 1.2 非目标（第一阶段）

- goroutine、channel、`sync`、数据竞争：不建模。M3 的 `-race` 只作为附加证据。
- GC、finalizer、`unsafe`、cgo、反射、`defer`/`recover` 的精细语义。
- 浮点数：遇到即标记函数不可模型化。
- 整个程序的完整验证。对象是**函数级**性质。

## 2. 核心设计原则

### P1. 模型可以过近似，不能欠近似（对找反例而言）

翻译中遇到不建模的东西（未知外部调用、未建模的内存操作、runtime 函数），一律用 **havoc**（显式的非确定输入）代替，不猜值、不放弃整个函数。

- 过近似下**找不到反例 ⇒ 真实程序也没有**（证明方向 sound）。
- 过近似下的反例**可能是假的**，由 M3 过滤。M3 是 oracle，M1 不必完美。
- 唯一允许的欠近似是循环 fuel，结果必须标记 `bounded_proved(fuel = N)`。

### P2. havoc 是显式参数

被 havoc 的值作为模型函数的**额外输入**，不是 `sorry` 或公理。`bv_decide`/`plausible` 能选它们，反例里带它们的取值，M3 判 `spurious` 时能说出"该反例依赖第 3 个 havoc（`call strings.Index` at `f.go:42`）"。

### P3. 性质带双形式：Lean 命题 + Go 可执行 oracle

每条性质同时携带在模型上的 Lean 命题和在真实代码上的 Go 检查。两者不一致是最危险的错误来源。缓解手段是 §5.2 的 **spec-in-Go**：agent 只写 Go 形式，Lean 形式由 M1 用同一条翻译管线机械生成，一致性由构造保证。

### P4. 反例先在模型内复验，再送 M3

从 Lean 输出解析出的反例，先用 `#eval` 在模型里重跑一次确认违反性质，再写入 `Counterexample`。

### P5. 内核与 dialect 分离

内核只知道"有类型的值、带参数的基本块、终结指令、fuel、havoc、panic"。dialect 提供 `Ty`、`Op`、`Ty` 到 Lean 类型的解释、单条 `Op` 的语义。这是 lean-mlir 的架构思路，但我们只取其 SSA 核心，不取其重写框架和 region 通用性。

## 3. 系统总览

```
                 ┌───────────────────────────────────────────────────┐
                 │ schemas/ 共享契约 (Target/Property/Counterexample/ │
                 │          Verdict/ProofResult/ModelGap)             │
                 └───────────────────────────────────────────────────┘
   Go 源码
     │
     ├─► F-GoSSA ──► GoSSA dialect 项 ─┐
     │                                 ├──► Lean SSA 内核 ──► 可执行模型 + abi.json
     └─► F-LLVM  ──► LLVM dialect 项 ──┘      (Go2Lean.SSA)        │
                                    ▲                            ▼
                      交叉对比 ◄─────┘                 ┌──────────────────┐ Counterexample ┌──────────┐
                                                       │ M2 agentic       │ ─────────────► │ M3 poc   │
                                                       │ 提性质/引擎阶梯   │ ◄───────────── │ go test  │
                                                       └──────────────────┘    Verdict     └──────────┘
                                                                ▲
                      M1 ◄───── ModelGap ───────────────────────┘
```

三个模块之间**只通过 JSON 和文件系统通信**，可独立运行和测试。

## 4. Lean SSA 内核（`Go2Lean.SSA`）

### 4.1 为什么要内核

- 两个前端共用 CFG 解释、fuel、havoc、panic 和值编码，前端 emitter 只负责"翻译指令"。
- 交叉对比在内核层面就是"同一个解释器跑两个程序"。
- 性质对两条路线写法相同。
- 以后接 Rust/C（经 LLVM）或别的 Go 前端，零内核改动。

### 4.2 结构（参考 lean-mlir，取其 SSA 核心）

```lean
namespace Go2Lean.SSA

/-- dialect 提供的东西 -/
class Dialect (d : Type) where
  Ty      : Type                       -- 类型语法
  Op      : Type                       -- 指令语法（含操作数引用）
  toType  : Ty → Type                  -- 类型解释，如 GoSSA.int → BitVec 64
  sig     : Op → List Ty × Ty          -- 操作数类型 / 结果类型
  denote  : (op : Op) → HList toType (sig op).1 → Havoc → Res (toType (sig op).2)
  -- denote 可以 panic、可以消费 havoc；不能分支、不能调用其他函数

/-- 内核的程序表示：带参数的基本块 + 终结指令 -/
inductive Term (d) : Type
  | ret   (v : Var)
  | jump  (target : BlockId) (args : List Var)
  | br    (cond : Var) (t f : BlockId) (targs fargs : List Var)
  | switch (v : Var) (cases : List (Lit × BlockId × List Var)) (default : BlockId × List Var)
  | panic (kind : PanicKind)
  | call  (f : FuncId) (args : List Var) (ret : Var) (next : BlockId) (nargs : List Var)

structure Block (d) where
  params : List (Var × Dialect.Ty d)
  instrs : List (Var × Dialect.Op d)
  term   : Term d

structure Func (d) where
  params : List (Var × Dialect.Ty d)
  ret    : Dialect.Ty d
  blocks : Array (Block d)
  entry  : BlockId

/-- 内核解释器：唯一的递归，带 fuel -/
def Func.run (prog : Prog d) (f : FuncId) (args : Env d) (h : Havoc) : Nat → Res (Val d)
```

要点：
- **phi 转块参数**：`go/ssa` 的 `Phi` 和 LLVM 的 `phi` 都转成块参数，内核不认识 phi。
- **`denote` 无分支无递归**：单条指令是纯函数（可 panic、可读 havoc）。所有控制流在 `Term`。这让 `simp` 能把 `Func.run` 按 fuel 展开成直线位向量表达式，交给 `bv_decide`——这是 lean-mlir 用 `simp_peephole` 归约到浅嵌入后再 `bv_decide` 的同一套路。
- **`Res α := ok α | panic PanicKind | outOfFuel`**。`outOfFuel` 是模型欠近似，`panic` 是真实可观察行为，严格区分。
- **`Havoc`**：按 `(site, iter)` 索引的非确定值。第一版 `Nat → Nat → Val`。
- 函数调用在 `Term.call`，由内核处理（递归消耗 fuel）。dialect 的 `denote` 不能调函数——这样 dialect 保持简单，且跨 dialect 的函数调用（GoSSA 调 LLVM 建模的 runtime）在内核层就能做。

深嵌入 + 指称语义 + simp 归约，而不是直接生成浅嵌入：多付一次内核的工作，换来两条前端共用一切、以及把"翻译"与"语义"分开可独立测试。

### 4.3 `GoSSA` dialect

对应 `go/ssa` 的指令集。`go/ssa` 构建时已经把未取地址的局部变量提升为 SSA 寄存器（相当于做过 mem2reg），只有逃逸/取地址的才有 `Alloc`/`Store`/`*`。

| go/ssa | GoSSA.Op / Term | 阶段 |
|---|---|---|
| `BinOp`（算术/比较/位运算/移位） | `Op.binop`；移位 ≥ 宽度按 Go 语义（结果 0 或符号位），负移位 panic | A |
| `UnOp`（`-`、`^`、`!`；`*` 是 load） | `Op.unop`；`*` 见内存 | A / B |
| `Convert`/`ChangeType` | `Op.conv`（整数宽度/符号变化） | A |
| `If`/`Jump`/`Return`/`Panic` | `Term` | A |
| `Phi` | 块参数 | A |
| `Call`（同包已建模函数） | `Term.call` | A |
| `Call`（未建模） | havoc 结果 | A |
| `Index`（数组值索引）、`Lookup`（string 索引） | `Op.index`，带越界 panic | A |
| `Slice`、`IndexAddr`、`MakeSlice`、`len`/`cap` | 见内存模型 | A（只读）/ B |
| `Field`/`Extract`/`MakeTuple` | 结构解包/多返回值 | A |
| `Alloc`/`Store`/`*`/`FieldAddr`/`MakeMap`/`MapUpdate` | 堆模型 | B |
| `MakeInterface`/`TypeAssert`/`ChangeInterface` | 第一版 havoc | C |
| `Go`/`Send`/`Recv`/`Select`/`Defer`/`RunDefers` | 不建模，函数标记 partial | — |

**类型解释**：`int`/`int64`/`uint64` → `BitVec 64`，`int32`/`uint32` → `BitVec 32`，…，`bool` → `Bool`，`string` → `(ref, len)` 内容走堆或 havoc。

**内存模型（这是 GoSSA 路线比 LLVM 路线干净得多的地方）**：
- **阶段 A（只读）**：slice 是 `(ref, off, len, cap)`，`ref` 抽象；读元素 = `h.load ref (off+i)`，是 havoc，但越界检查只看 `len`，所以 **越界性质不需要内存内容**。对只读 slice 的函数，这已经是精确模型（havoc 的值被 M3 用 `go_hint` 具体化，见 §4.6）。写 slice 元素 → 函数标记 partial。
- **阶段 B（堆）**：`Heap := Ref → Obj`，`Obj` 是定长 `Array Val`（数组/切片底层数组/结构体）或 `Map`。`Store`/`*`/`IndexAddr` 变成堆上的读写，别名天然正确。`MakeSlice` 分配新 `Ref`。函数签名增加 `Heap` 输入/输出。
- **阶段 C**：interface、闭包、map 迭代顺序（havoc）。

### 4.4 `LLVM` dialect

| LLVM | LLVM.Op / Term | 阶段 |
|---|---|---|
| `add sub mul udiv sdiv urem srem shl lshr ashr and or xor icmp select zext sext trunc` | `Op`；`sdiv`/`srem` 的除零与 `MinInt/-1` 在 LLVM 是 UB——翻译时插入 Go 语义的 panic，记入 model-report | A |
| `br condbr switch ret unreachable phi call` | `Term`；`call @runtime.*Panic` 后接 `unreachable` → `Term.panic` | A |
| `llvm.*.with.overflow` | `Op` | A |
| `getelementptr load store alloca` | 阶段 A 全 havoc；阶段 C 平坦字节内存 | A / C |

**管线**：`tinygo build` 导出 IR（`-o x.ll` 或 `-printir`，**待验证**）→ `opt -passes=mem2reg,instnamer` 归一化 → `github.com/llir/llvm` 解析 → emitter。**target 必须固定**为 64 位本机 target，`int` 宽度与 `go test` 一致；排除 wasm（i64 有特殊 ABI）。

这条路线的价值：(1) 其他 LLVM 语言的入口；(2) 对 GoSSA 路线的交叉验证；(3) 捕获 TinyGo 与 gc 的语义差异。它的内存模型比 GoSSA 弱得多，所以**内存相关性质以 GoSSA 为主**。

### 4.5 前端 emitter（Go 侧）

```
internal/frontend/gossa/   go/packages + go/ssa → GoSSA 项
internal/frontend/llvm/    tinygo + opt + llir  → LLVM 项
internal/ssaout/           共用：Lean 代码生成（打印内核项）、abi.json、model-report.json
```

两个 emitter 输出同一种中间结构（Go 结构体版的 `Func`/`Block`/`Term`，op 用字符串 + 操作数），`ssaout` 负责打印成 Lean。这样 Lean 代码生成只写一次。

### 4.6 ABI 桥：`abi.json`

M3 要把反例中的模型值翻译回 Go 值。GoSSA 路线这张表几乎是平凡的（类型就是 Go 类型）；LLVM 路线要按 TinyGo 展开规则（slice → `ptr,len,cap`，string → `ptr,len`，struct 展开字段，多返回值 → LLVM struct）。两条路线输出同一个 schema：

```json
{
  "go_func": "example.com/m/examples/overflow.Sum",
  "go_sig": "func(xs []int) int",
  "frontend": "gossa",
  "params": [{"go": "xs", "go_type": "[]int", "model": ["xs_ref", "xs_off", "xs_len", "xs_cap"], "kind": "slice", "elem": "int"}],
  "result": {"go_type": "int"},
  "havoc": [{"idx": 0, "site": "loop.body: *IndexAddr xs i", "reason": "slice-load", "go_hint": "xs[i]"}]
}
```

`go_hint` 让 M3 能把 havoc 取值**真的填进 slice**，阶段 A 的 havoc 反例因此大多不是 spurious。

### 4.7 产出

- `lean/Go2Lean/Generated/<frontend>/<pkg>.lean`
- `out/<pkg>/<frontend>/abi.json`、`model-report.json`（每个函数：完整/partial/拒绝；havoc 列表；插入的语义补丁）
- `out/<pkg>/crosscheck.json`（两条前端都有的函数，随机输入对比结果）

## 5. M2 agentic：性质与反例

### 5.1 输入

Go 源码 + 类型信息、`model-report.json`、`abi.json`、`crosscheck.json`、可选的用户自然语言关注点。

### 5.2 性质种类与 spec-in-Go

| kind | 含义 | Lean 形式 | Go oracle |
|---|---|---|---|
| `no_panic` | 前置条件下不 panic | `∀ args h fuel, pre → run f args h fuel ≠ .panic _` | 机械生成：`defer recover()` |
| `postcondition` | 返回值满足谓词 | 由 `spec` 函数翻译得到 | agent 写的 `Spec(args..., ret) bool` |
| `equivalence` | `f ≡ g` | `∀ args, run f = run g` | 跑两者比较 |
| `crosscheck` | GoSSA 模型 ≡ LLVM 模型 | 内核层比较 | 真实 `go test` vs `tinygo test` |
| `custom` | agent 直接写 Lean | agent 写 | agent 写（必须） |

**spec-in-Go**：agent 写 Go 函数 `// go2lean:spec Sum` `func SpecSum(xs []int, ret int) bool`。M1 用同一条管线把 spec 编进模型，性质是 `run Sum args = .ok r → run SpecSum (args, r) = .ok true`。Lean 形式与 Go oracle 由构造一致，agent 不需要写 Lean。`custom` 是 agent 唯一直接写 Lean 的地方，反例单独标记。

### 5.3 引擎阶梯

1. `plausible`：随机测试 + 收缩，秒级，任何可 `#eval` 的模型都能跑。
2. `bv_decide`：固定 fuel 用 `simp [Func.run, denote, ...]` 展开成位向量表达式后判定，完备，给精确赋值。
3. 证明：`simp`/`omega`/`bv_decide`/`decide`，然后 agent 写的 Lean 证明。
4. 可选：导出 SMT-LIB 给 Z3/cvc5。不在第一阶段。

**反例提取**：第一版解析 `lean --json` 输出；并在 Lean 侧写一个 `#eval` 驱动，用 `Plausible.SampleableExt` 随机搜索并直接打印 JSON（待验证 API），减少对错误消息格式的依赖。无论来源，都走 P4 复验。

### 5.4 Agent 循环

```
loop:
  1. 读 model-report / abi / crosscheck / 源码 / 已有 Verdict
  2. 提出 N 条性质（排序：可检查性 × 预期 bug 产出率；crosscheck 不一致的函数优先）
  3. 每条跑引擎阶梯
  4. Counterexample 交给 M3，等 Verdict
  5. spurious：诊断指向 havoc 且有 go_hint → 让 M3 具体化重试；
              指向未建模调用 → 写 ModelGap，调性质前置条件或放弃；
              性质写错 → 修性质
  6. confirmed：产出报告；考虑泛化到相邻函数
  7. unknown：决定是否投入证明
```

agent 通过 CLI 子命令工作（§8），每步可日志、可重放。LLM 默认 Claude，Go 侧封装成 interface，便于 mock。

## 6. M3 poc：反例 → 真实验证

### 6.1 测试生成

在 Go 模块临时副本里，与目标函数同包生成 `zz_go2lean_poc_<id>_test.go`：

```go
func TestGo2LeanPoC_cx0042(t *testing.T) {
    xs := []int{9223372036854775807, 1}   // 反例 + abi 还原，havoc 经 go_hint 具体化
    var ret int
    panicked, pv := go2leanCatch(func() { ret = Sum(xs) })
    if panicked { t.Fatalf("unexpected panic: %v", pv) }
    if SpecSum(xs, ret) { t.Fatalf("spec held; spurious (ret=%d)", ret) }
}
```

`no_panic` 的 confirmed 条件是真的 panic 且类别匹配（runtime error 文本可区分越界/除零/nil）。

### 6.2 运行与判定

`go test -count=1 -run '^TestGo2LeanPoC_cx0042$' ./pkg`，固定超时；`crosscheck` 类性质再跑 `tinygo test`。

| status | 条件 |
|---|---|
| `confirmed` | 真实运行违反 Go oracle，且方式与 kind 一致 |
| `spurious` | 真实运行满足 oracle |
| `inconclusive` | 编译失败/超时/反例无法具体化 |

`spurious` 附诊断（模型输出 vs 真实输出、依赖的 havoc、是否 `custom`），是回灌 M1/M2 的唯一渠道。`confirmed` 产出 `out/poc/<id>/`：测试文件、复现命令、性质描述、模型 trace。

## 7. 共享契约

JSON，schema 在 `schemas/`，Go 类型在 `internal/contract/`。整数一律十进制字符串。

```jsonc
// Target
{"id":"t-sum","module":"example.com/m","package":"examples/overflow","func":"Sum",
 "go_sig":"func(xs []int) int","models":{"gossa":"out/.../gossa/abi.json","llvm":"out/.../llvm/abi.json"}}

// Property
{"id":"p-0007","target":"t-sum","kind":"postcondition","nl":"所有元素非负时和非负",
 "precondition":{"go":"allNonNeg(xs)"},"spec":{"go_func":"SpecSum"},
 "frontend":"gossa","source":"agent"}

// Counterexample
{"id":"cx-0042","property":"p-0007","frontend":"gossa","engine":"bv_decide","fuel":8,
 "inputs":{"xs_len":"2","xs_cap":"2"},
 "havoc":[{"idx":0,"iter":0,"value":"9223372036854775807","go_hint":"xs[i]"},
          {"idx":0,"iter":1,"value":"1","go_hint":"xs[i]"}],
 "model_output":{"res":"ok","value":"-9223372036854775808"},"revalidated_in_lean":true}

// Verdict
{"counterexample":"cx-0042","status":"confirmed",
 "evidence":{"cmd":"go test ...","exit":1,"stdout_tail":"..."},
 "observed":{"ret":"-9223372036854775808"},"diagnosis":null,"poc_dir":"out/poc/cx-0042"}

// ProofResult
{"property":"p-0008","status":"bounded_proved","fuel":16,"tactic":"bv_decide",
 "lean_file":"lean/Go2Lean/Proofs/p-0008.lean","time_ms":3200}

// ModelGap
{"from_verdict":"cx-0050","frontend":"gossa","kind":"unmodeled-call",
 "site":"f.go:42 call strings.Index","suggestion":"model as length-bounded havoc"}
```

## 8. 仓库布局与 CLI

```
go2lean/
  cmd/go2lean/
  internal/contract/          §7 类型 + 校验
  internal/frontend/gossa/    F-GoSSA
  internal/frontend/llvm/     F-LLVM（tinygo/opt/llir）
  internal/ssaout/            共用：内核项打印、abi、model-report
  internal/crosscheck/        双前端对比
  internal/engines/           lean 进程封装、输出解析、模型内复验
  internal/agent/             M2 循环、LLM 接口
  internal/poc/               M3
  lean/                       lake 项目（Lean 4 core + plausible；不引入 Mathlib）
    Go2Lean/SSA/              内核：Dialect、Func、Term、Res、Havoc、解释器、simp 引理
    Go2Lean/Dialect/GoSSA/    GoSSA 的 Ty/Op/denote
    Go2Lean/Dialect/LLVM/     LLVM 的 Ty/Op/denote
    Go2Lean/Generated/        M1 输出（不手改）
    Go2Lean/Proofs/           M2 产出
  schemas/
  examples/                   真值集
  docs/
```

```
go2lean compile  ./examples/overflow --frontend gossa|llvm|both
go2lean crosscheck ./examples/overflow
go2lean props    --target t-sum --propose
go2lean hunt     --property p-0007 --engine plausible|bv_decide|all
go2lean prove    --property p-0008
go2lean poc      --counterexample cx-0042
go2lean run      ./examples/overflow
```

## 9. 真值集与度量

`examples/` 每个样例含 bug 版、修复版、人写性质、预期结果。第一批：

| 样例 | bug | 阶段 |
|---|---|---|
| `overflow` | `len * size` 溢出为负 | A |
| `divzero` | 用户输入做除数 | A |
| `oob` | `i <= len` off-by-one | A（越界只看 len） |
| `shift` | 移位量 ≥ 64 / 负移位 | A |
| `lenfield` | 不可信长度字段驱动 `make`/切片 | A/B |
| `alias` | 两个 slice 别名下的写入 | B（只有 GoSSA 能做） |
| `safe_*` | 修复版，期望 `proved`/`bounded_proved` | A |

度量：反例召回率、`spurious` 率、`unknown` 率、证明率、crosscheck 一致率、端到端时间。

## 10. 分期

- **M0 – 契约 + M3 骨架 + 内核雏形**：schemas、`internal/contract`、`internal/poc`；Lean 侧写出 `SSA` 内核和 `GoSSA` dialect 的阶段 A 子集，**手写**一个 `Generated/gossa/overflow.lean`，构造一个 `Counterexample`，跑出第一个 `confirmed`。目的：闭环的可信端和语义端都先成立，再做翻译。
- **M1a – F-GoSSA 阶段 A**：`go/ssa` 前端、`ssaout` 打印、`plausible` 引擎。`overflow`/`divzero`/`shift`/`oob` 端到端。
- **M1b – `bv_decide` 与具体化**：simp 引理集（`Func.run` 按 fuel 展开）、`bv_decide` 引擎、反例解析与复验、`go_hint` 具体化。
- **M2a – agent**：spec-in-Go、性质提议、引擎阶梯、Verdict 反馈。`safe_*` 得到 `bounded_proved`。
- **M1c – F-LLVM 阶段 A + crosscheck**：TinyGo spike（解决所有待验证）、llir 解析、LLVM dialect、交叉对比。
- **M1d – GoSSA 堆模型（阶段 B）**；**M2b – `custom` 性质与证明 agent**。
- **之后**：LLVM 字节内存、第二个 LLVM 前端语言、interface/闭包、并发。

## 11. 主要风险

| 风险 | 影响 | 对策 |
|---|---|---|
| 内核 + dialect 的 Lean 工程量 | M0 变长 | 严格限定阶段 A 指令集；不做 region/重写框架；先手写 Generated 验证内核能用再写 emitter |
| `simp` 展开 `Func.run` 后 `bv_decide` 爆炸 | 循环性质只能 `plausible` | 小 fuel；循环不变量交证明 agent；把 `denote` 保持为无分支纯函数以便归约 |
| `go/ssa` 的指令语义细节（移位、转换、字符串索引） | 模型与 Go 不一致 | 真值集 + M3 兜底 + crosscheck |
| TinyGo IR 结构被优化破坏 | LLVM 路线可读性差 | 低 `-opt`、比较各阶段 IR；该路线本来就是次要路线 |
| spec 函数超出子集 | 性质编不进模型 | spec 必须在子集内；model-report 报告 spec 状态，agent 据此重写 |
| 反例解析依赖 Lean 输出格式 | 版本升级即坏 | 自制 `#eval` JSON 驱动 + P4 复验 |
| `custom` 性质双形式不一致 | 假阳/假阴 | 单独标记、最后考虑、每个 confirmed 都靠 M3 |

## 12. 已定 / 待定

已定（本轮）：
- M1 双前端，共用 Lean SSA 内核；`go/ssa` 先行，LLVM 随后作为交叉验证与多语言入口。
- Lean 侧不引入 Mathlib。

待你确认：
1. **内核采用深嵌入 + 指称语义**（§4.2），而非每个前端直接生成浅嵌入。代价是 M0 的 Lean 工作量，收益是两前端共用一切。
2. **阶段 A 的 slice 只读**（写元素即 partial），堆模型放 M1d。
3. **M0 先做 M3 与内核雏形**，再做 `go/ssa` emitter。

## 13. 参考

- lean-mlir（Lean 中的 SSA 理论与 LLVM 方言语义；本设计的内核思路来源）：https://github.com/opencompl/lean-mlir
- `golang.org/x/tools/go/ssa`：https://pkg.go.dev/golang.org/x/tools/go/ssa
- TinyGo 调用约定（slice/string/interface 展开为标量）：https://tinygo.org/docs/concepts/compiler-internals/calling-convention/
- TinyGo 数据类型布局：https://tinygo.org/docs/concepts/compiler-internals/datatypes/
- TinyGo 编译管线：https://tinygo.org/docs/concepts/compiler-internals/pipeline/
- Lean 4.12 `bv_decide`（内置 CaDiCaL，反例赋值输出）：https://lean-lang.org/blog/2024-10-3-lean-4120
- `plausible`（Lean 4 属性测试）：https://reservoir.lean-lang.org/@leanprover-community/plausible
- llir/llvm（纯 Go 的 LLVM IR 解析库）：https://github.com/llir/llvm
