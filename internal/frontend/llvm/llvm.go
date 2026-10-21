//go:build llvm15 || llvm16 || llvm17 || llvm18 || llvm19 || llvm20 || llvm21 || llvm22 || llvm23

// Package llvm is go2lean's LLVM frontend: it reads LLVM IR (bitcode or
// text, as clang emits it at -O0 with mem2reg applied) with go-llvm and
// prints each defined function as a Lean model over the LLVM dialect of
// lean-llvm/, whose instruction semantics are VeIR's.
//
// It is built only with one of go-llvm's version tags (-tags llvm18), since
// it links libLLVM through cgo; without one, Available is false.
package llvm

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"tinygo.org/x/go-llvm"
)

// Available says whether this binary was built with the LLVM frontend.
const Available = true

// Gen is how the search generates one parameter; the shape mirrors
// Go2Lean.LLVM.Search.Gen.
type Gen struct {
	K      string `json:"k"`
	Name   string `json:"name"`
	W      int    `json:"w,omitempty"`
	EB     int    `json:"eb,omitempty"`
	MaxLen int    `json:"maxLen,omitempty"`
	Of     int    `json:"of"`
}

// Func is one lowered function: its index in the model, how its
// parameters are generated, and what the lowering did not model.
type Func struct {
	Name   string
	Index  int
	Gens   []Gen
	Issues []string
}

// Model is a lowered module: its functions and the Lean definitions.
type Model struct {
	Funcs []Func
	// Lean is the body of the model's module: one definition per function
	// and `prog`.
	Lean string
}

// maxBuf bounds the elements of a generated buffer.
const maxBuf = 8

// Compile turns a C file into the bitcode the frontend reads: clang at -O0
// without optnone, then mem2reg, so values are SSA registers and memory is
// what the source addresses. A .ll or .bc file is used as it is.
func Compile(src, dir string) (string, error) {
	switch filepath.Ext(src) {
	case ".ll", ".bc":
		return src, nil
	case ".c":
	default:
		return "", fmt.Errorf("%s: want a .c, .ll or .bc file", src)
	}
	base := filepath.Join(dir, strings.TrimSuffix(filepath.Base(src), ".c"))
	run := func(name string, args ...string) error {
		out, err := exec.Command(name, args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s: %v\n%s", name, err, out)
		}
		return nil
	}
	if err := run("clang", "-O0", "-Xclang", "-disable-O0-optnone", "-g0", "-emit-llvm", "-c", src, "-o", base+".O0.bc"); err != nil {
		return "", err
	}
	if err := run("opt", "-passes=mem2reg", base+".O0.bc", "-o", base+".bc"); err != nil {
		return "", err
	}
	return base + ".bc", nil
}

// Load reads bitcode or IR text and lowers every defined function.
func Load(path string) (*Model, error) {
	ctx := llvm.NewContext()
	buf, err := llvm.NewMemoryBufferFromFile(path)
	if err != nil {
		return nil, err
	}
	m, err := ctx.ParseIR(buf)
	if err != nil {
		return nil, err
	}
	l := &lowerer{td: llvm.NewTargetData(m.DataLayout()), fids: map[string]int{}}
	var defs []llvm.Value
	for f := m.FirstFunction(); !f.IsNil(); f = llvm.NextFunction(f) {
		if !f.IsDeclaration() {
			l.fids[f.Name()] = len(defs)
			defs = append(defs, f)
		}
	}
	model := &Model{}
	var b strings.Builder
	var names []string
	for i, f := range defs {
		text, fn := l.function(f, i)
		model.Funcs = append(model.Funcs, fn)
		b.WriteString(text)
		names = append(names, fmt.Sprintf("f%d", i))
	}
	widths := make([]string, len(l.havoc))
	for i, w := range l.havoc {
		widths[i] = fmt.Sprint(w)
	}
	fmt.Fprintf(&b, "def prog : Go2Lean.LLVM.Prog := { funcs := #[%s], havocWidths := #[%s] }\n",
		strings.Join(names, ", "), strings.Join(widths, ", "))
	model.Lean = b.String()
	return model, nil
}

// WriteModule writes the model as module `Go2Lean.Generated.llvm.<key>`.
func WriteModule(model *Model, leanDir, key string) error {
	dir := filepath.Join(leanDir, "Go2Lean", "Generated", "llvm")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	mod := "Go2Lean.Generated.llvm." + key
	src := fmt.Sprintf("import Go2LeanLLVM.Dialect\n\nnamespace %s\n\nopen Go2Lean.SSA Go2Lean.LLVM\n\n%s\nend %s\n", mod, model.Lean, mod)
	return os.WriteFile(filepath.Join(dir, key+".lean"), []byte(src), 0o644)
}

type lowerer struct {
	td    llvm.TargetData
	fids  map[string]int
	havoc []int // width of each havoc site
}

type block struct {
	params []int
	instrs []string
	term   string
}

type fnState struct {
	l       *lowerer
	vars    map[llvm.Value]int
	next    int
	blocks  []*block
	bbIndex map[llvm.BasicBlock]int
	cur     *block
	curBB   llvm.BasicBlock
	issues  []string
}

func (t *fnState) fresh() int { t.next++; return t.next - 1 }

func (t *fnState) emit(dst int, op string, args ...int) {
	t.cur.instrs = append(t.cur.instrs, fmt.Sprintf(".op %d %s %s", dst, op, list(args)))
}

func (t *fnState) issue(format string, args ...any) {
	t.issues = append(t.issues, fmt.Sprintf(format, args...))
}

func list(vs []int) string {
	s := make([]string, len(vs))
	for i, v := range vs {
		s[i] = fmt.Sprint(v)
	}
	return "[" + strings.Join(s, ", ") + "]"
}

func lint(v int64) string {
	if v < 0 {
		return fmt.Sprintf("(%d)", v)
	}
	return fmt.Sprint(v)
}

func (t *fnState) newBlock() int {
	t.blocks = append(t.blocks, &block{})
	return len(t.blocks) - 1
}

// val is the variable holding operand v, materialising constants.
func (t *fnState) val(v llvm.Value) int {
	if id, ok := t.vars[v]; ok {
		return id
	}
	d := t.fresh()
	ty := v.Type()
	switch {
	case !v.IsAConstantInt().IsNil() && ty.IntTypeWidth() <= 64:
		t.emit(d, fmt.Sprintf("(.const %d %s)", ty.IntTypeWidth(), lint(v.SExtValue())))
	case ty.TypeKind() == llvm.PointerTypeKind && v.IsNull():
		t.emit(d, ".null")
	case !v.IsAUndefValue().IsNil() && ty.TypeKind() == llvm.IntegerTypeKind:
		t.emit(d, fmt.Sprintf("(.poison %d)", ty.IntTypeWidth()))
	default:
		what := strings.TrimSpace(v.String())
		t.issue("operand %s not modelled", what)
		t.emit(d, fmt.Sprintf("(.unsupported %q)", "operand "+what))
	}
	return d
}

// flags reads nsw, nuw and exact from the instruction's text (go-llvm has
// no accessors for them).
func flags(in llvm.Value) (nsw, nuw, exact bool) {
	s := in.String()
	if i := strings.Index(s, "="); i >= 0 {
		s = s[i+1:]
	}
	fs := strings.Fields(s)
	for _, f := range fs[1:] {
		switch f {
		case "nsw":
			nsw = true
		case "nuw":
			nuw = true
		case "exact":
			exact = true
		default:
			return
		}
	}
	return
}

func bool2(b bool) string { return fmt.Sprint(b) }

var preds = map[llvm.IntPredicate]string{
	llvm.IntEQ: "eq", llvm.IntNE: "ne", llvm.IntUGT: "ugt", llvm.IntUGE: "uge", llvm.IntULT: "ult",
	llvm.IntULE: "ule", llvm.IntSGT: "sgt", llvm.IntSGE: "sge", llvm.IntSLT: "slt", llvm.IntSLE: "sle",
}

// edgeArgs are the values block succ's phis take on the edge from pred.
func (t *fnState) edgeArgs(pred, succ llvm.BasicBlock) []int {
	var args []int
	for in := succ.FirstInstruction(); !in.IsNil() && in.InstructionOpcode() == llvm.PHI; in = llvm.NextInstruction(in) {
		for i := 0; i < in.IncomingCount(); i++ {
			if in.IncomingBlock(i) == pred {
				args = append(args, t.val(in.IncomingValue(i)))
				break
			}
		}
	}
	return args
}

func (t *fnState) jumpTo(succ llvm.BasicBlock) string {
	return fmt.Sprintf(".jump %d %s", t.bbIndex[succ], list(t.edgeArgs(t.curBB, succ)))
}

// elemBytes guesses the element size of what pointer parameter p points
// to: the stride of the first getelementptr on it, else the width of the
// first load or store through it, else 1.
func (l *lowerer) elemBytes(f, p llvm.Value) int {
	for b := f.FirstBasicBlock(); !b.IsNil(); b = llvm.NextBasicBlock(b) {
		for in := b.FirstInstruction(); !in.IsNil(); in = llvm.NextInstruction(in) {
			switch in.InstructionOpcode() {
			case llvm.GetElementPtr:
				if in.Operand(0) == p {
					return int(l.td.TypeAllocSize(in.GEPSourceElementType()))
				}
			case llvm.Load:
				if in.Operand(0) == p && in.Type().TypeKind() == llvm.IntegerTypeKind {
					return max(1, in.Type().IntTypeWidth()/8)
				}
			case llvm.Store:
				if in.Operand(1) == p && in.Operand(0).Type().TypeKind() == llvm.IntegerTypeKind {
					return max(1, in.Operand(0).Type().IntTypeWidth()/8)
				}
			}
		}
	}
	return 1
}

func (l *lowerer) function(f llvm.Value, index int) (string, Func) {
	t := &fnState{l: l, vars: map[llvm.Value]int{}, bbIndex: map[llvm.BasicBlock]int{}}
	fn := Func{Name: f.Name(), Index: index}
	var params []int
	for i, p := range f.Params() {
		t.vars[p] = t.fresh()
		params = append(params, t.vars[p])
		name := p.Name()
		if name == "" {
			name = fmt.Sprintf("arg%d", i)
		}
		switch ty := p.Type(); ty.TypeKind() {
		case llvm.IntegerTypeKind:
			g := Gen{K: "int", Name: name, W: ty.IntTypeWidth()}
			// An integer right after a pointer is that buffer's length.
			if i > 0 && len(fn.Gens) == i && fn.Gens[i-1].K == "buf" {
				g = Gen{K: "len", Name: name, W: ty.IntTypeWidth(), Of: i - 1}
			}
			fn.Gens = append(fn.Gens, g)
		case llvm.PointerTypeKind:
			fn.Gens = append(fn.Gens, Gen{K: "buf", Name: name, EB: l.elemBytes(f, p), MaxLen: maxBuf})
		default:
			t.issue("parameter %d of type %s not modelled", i, ty.String())
		}
	}
	// Every LLVM block gets its block now, so branches can name it; calls
	// and switches add blocks after them.
	for b := f.FirstBasicBlock(); !b.IsNil(); b = llvm.NextBasicBlock(b) {
		t.bbIndex[b] = t.newBlock()
		for in := b.FirstInstruction(); !in.IsNil() && in.InstructionOpcode() == llvm.PHI; in = llvm.NextInstruction(in) {
			t.vars[in] = t.fresh()
			t.blocks[t.bbIndex[b]].params = append(t.blocks[t.bbIndex[b]].params, t.vars[in])
		}
	}
	for b := f.FirstBasicBlock(); !b.IsNil(); b = llvm.NextBasicBlock(b) {
		t.curBB = b
		t.cur = t.blocks[t.bbIndex[b]]
		for in := b.FirstInstruction(); !in.IsNil(); in = llvm.NextInstruction(in) {
			t.instr(in)
		}
	}
	fn.Issues = t.issues
	var sb strings.Builder
	fmt.Fprintf(&sb, "def f%d : Go2Lean.LLVM.Func := {\n  name := %q, params := %s, nvals := %d, entry := 0,\n  blocks := #[\n", index, f.Name(), list(params), t.next)
	for i, blk := range t.blocks {
		sep := ","
		if i == len(t.blocks)-1 {
			sep = ""
		}
		term := blk.term
		if term == "" {
			term = ".panic .modelStuck"
		}
		fmt.Fprintf(&sb, "    { params := %s, instrs := #[%s], term := %s }%s\n", list(blk.params), strings.Join(blk.instrs, ", "), term, sep)
	}
	sb.WriteString("  ] }\n\n")
	return sb.String(), fn
}

func (t *fnState) instr(in llvm.Value) {
	op := in.InstructionOpcode()
	ty := in.Type()
	switch op {
	case llvm.PHI:
		return // block parameters
	case llvm.Add, llvm.Sub, llvm.Mul:
		nsw, nuw, _ := flags(in)
		name := map[llvm.Opcode]string{llvm.Add: "add", llvm.Sub: "sub", llvm.Mul: "mul"}[op]
		// From C, nsw and nuw come from signed / unsigned arithmetic whose
		// overflow C makes undefined (nuw only for pointer arithmetic).
		a, b := t.val(in.Operand(0)), t.val(in.Operand(1))
		t.vars[in] = t.fresh()
		t.emit(t.vars[in], fmt.Sprintf("(.%s %s %s %s)", name, bool2(nsw), bool2(nuw), bool2(nsw)), a, b)
	case llvm.UDiv, llvm.SDiv, llvm.LShr, llvm.AShr:
		_, _, exact := flags(in)
		name := map[llvm.Opcode]string{llvm.UDiv: "udiv", llvm.SDiv: "sdiv", llvm.LShr: "lshr", llvm.AShr: "ashr"}[op]
		a, b := t.val(in.Operand(0)), t.val(in.Operand(1))
		t.vars[in] = t.fresh()
		t.emit(t.vars[in], fmt.Sprintf("(.%s %s)", name, bool2(exact)), a, b)
	case llvm.Shl:
		nsw, nuw, _ := flags(in)
		a, b := t.val(in.Operand(0)), t.val(in.Operand(1))
		t.vars[in] = t.fresh()
		t.emit(t.vars[in], fmt.Sprintf("(.shl %s %s)", bool2(nsw), bool2(nuw)), a, b)
	case llvm.URem, llvm.SRem, llvm.And, llvm.Or, llvm.Xor:
		name := map[llvm.Opcode]string{llvm.URem: "urem", llvm.SRem: "srem", llvm.And: "and", llvm.Or: "or", llvm.Xor: "xor"}[op]
		a, b := t.val(in.Operand(0)), t.val(in.Operand(1))
		t.vars[in] = t.fresh()
		t.emit(t.vars[in], "."+name, a, b)
	case llvm.ICmp:
		a, b := t.val(in.Operand(0)), t.val(in.Operand(1))
		t.vars[in] = t.fresh()
		name := "icmp"
		if in.Operand(0).Type().TypeKind() == llvm.PointerTypeKind {
			name = "icmpPtr"
		}
		t.emit(t.vars[in], fmt.Sprintf("(.%s .%s)", name, preds[in.IntPredicate()]), a, b)
	case llvm.Select:
		c, a, b := t.val(in.Operand(0)), t.val(in.Operand(1)), t.val(in.Operand(2))
		t.vars[in] = t.fresh()
		t.emit(t.vars[in], ".select", c, a, b)
	case llvm.Trunc, llvm.ZExt, llvm.SExt:
		name := map[llvm.Opcode]string{llvm.Trunc: "trunc", llvm.ZExt: "zext", llvm.SExt: "sext"}[op]
		a := t.val(in.Operand(0))
		t.vars[in] = t.fresh()
		t.emit(t.vars[in], fmt.Sprintf("(.%s %d)", name, ty.IntTypeWidth()), a)
	case llvm.Alloca:
		n := t.l.td.TypeAllocSize(in.AllocatedType())
		if c := in.Operand(0); !c.IsAConstantInt().IsNil() {
			n *= c.ZExtValue()
		} else {
			t.issue("alloca of a dynamic count not modelled")
			t.vars[in] = t.fresh()
			t.emit(t.vars[in], `(.unsupported "dynamic alloca")`)
			return
		}
		t.vars[in] = t.fresh()
		t.emit(t.vars[in], fmt.Sprintf("(.alloca %d)", n))
	case llvm.Load:
		p := t.val(in.Operand(0))
		t.vars[in] = t.fresh()
		switch ty.TypeKind() {
		case llvm.IntegerTypeKind:
			t.emit(t.vars[in], fmt.Sprintf("(.load %d)", ty.IntTypeWidth()), p)
		case llvm.PointerTypeKind:
			t.emit(t.vars[in], ".loadPtr", p)
		default:
			t.issue("load of %s not modelled", ty.String())
			t.emit(t.vars[in], fmt.Sprintf("(.unsupported %q)", "load "+ty.String()), p)
		}
	case llvm.Store:
		v, p := t.val(in.Operand(0)), t.val(in.Operand(1))
		t.emit(t.fresh(), ".store", v, p)
	case llvm.GetElementPtr:
		t.gep(in)
	case llvm.Call:
		t.call(in)
	case llvm.Ret:
		if in.OperandsCount() == 0 {
			u := t.fresh()
			t.emit(u, "(.const 1 0)")
			t.cur.term = fmt.Sprintf(".ret %d", u)
			return
		}
		t.cur.term = fmt.Sprintf(".ret %d", t.val(in.Operand(0)))
	case llvm.Br:
		if in.OperandsCount() == 1 {
			t.cur.term = t.jumpTo(in.Successor(0))
			return
		}
		c := t.fresh()
		t.emit(c, ".cond", t.val(in.Operand(0)))
		tb, fb := in.Successor(0), in.Successor(1)
		t.cur.term = fmt.Sprintf(".br %d %d %s %d %s", c, t.bbIndex[tb], list(t.edgeArgs(t.curBB, tb)), t.bbIndex[fb], list(t.edgeArgs(t.curBB, fb)))
	case llvm.Switch:
		t.switchInstr(in)
	case llvm.Unreachable:
		u := t.fresh()
		t.emit(u, ".unreachable")
		t.cur.term = fmt.Sprintf(".ret %d", u)
	default:
		what := strings.TrimSpace(in.String())
		t.issue("instruction %s not modelled", what)
		d := t.fresh()
		t.vars[in] = d
		t.emit(d, fmt.Sprintf("(.unsupported %q)", what))
	}
}

// gep folds constant indices into an offset and passes the others with
// their strides.
func (t *fnState) gep(in llvm.Value) {
	p := t.val(in.Operand(0))
	cur := in.GEPSourceElementType()
	var offset int64
	var idxs []int
	var strides []string
	for i := 1; i < in.OperandsCount(); i++ {
		idx := in.Operand(i)
		var stride int64
		if i == 1 {
			stride = int64(t.l.td.TypeAllocSize(cur))
		} else {
			switch cur.TypeKind() {
			case llvm.StructTypeKind:
				k := int(idx.ZExtValue())
				offset += int64(t.l.td.ElementOffset(cur, k))
				cur = cur.StructElementTypes()[k]
				continue
			case llvm.ArrayTypeKind:
				cur = cur.ElementType()
				stride = int64(t.l.td.TypeAllocSize(cur))
			default:
				t.issue("getelementptr into %s not modelled", cur.String())
				t.vars[in] = t.fresh()
				t.emit(t.vars[in], `(.unsupported "getelementptr")`)
				return
			}
		}
		if !idx.IsAConstantInt().IsNil() {
			offset += idx.SExtValue() * stride
			continue
		}
		idxs = append(idxs, t.val(idx))
		strides = append(strides, lint(stride))
	}
	t.vars[in] = t.fresh()
	t.emit(t.vars[in], fmt.Sprintf("(.gep %s [%s])", lint(offset), strings.Join(strides, ", ")), append([]int{p}, idxs...)...)
}

// call: a call to a function of the module is the core's call terminator
// (the block ends there; a fallback block havocs the result if the callee
// gets stuck); a call to a declaration is havoc, its effects unmodelled;
// debug and lifetime intrinsics are dropped.
func (t *fnState) call(in llvm.Value) {
	callee := in.CalledValue()
	name := callee.Name()
	if strings.HasPrefix(name, "llvm.dbg.") || strings.HasPrefix(name, "llvm.lifetime.") {
		return
	}
	ty := in.Type()
	var args []int
	for i := 0; i < in.OperandsCount()-1; i++ {
		args = append(args, t.val(in.Operand(i)))
	}
	d := t.fresh()
	if ty.TypeKind() != llvm.VoidTypeKind {
		t.vars[in] = d
	}
	fid, known := t.l.fids[name]
	if !known {
		if ty.TypeKind() == llvm.IntegerTypeKind {
			t.issue("call %s: result is havoc, effects not modelled", name)
			t.l.havoc = append(t.l.havoc, ty.IntTypeWidth())
			t.cur.instrs = append(t.cur.instrs, fmt.Sprintf(".havoc %d %d", d, len(t.l.havoc)-1))
		} else if ty.TypeKind() == llvm.VoidTypeKind {
			t.issue("call %s: effects not modelled", name)
		} else {
			t.issue("call %s returning %s not modelled", name, ty.String())
			t.emit(d, fmt.Sprintf("(.unsupported %q)", "call "+name))
		}
		return
	}
	next, fb := t.newBlock(), t.newBlock()
	w := 64
	if ty.TypeKind() == llvm.IntegerTypeKind {
		w = ty.IntTypeWidth()
	}
	t.l.havoc = append(t.l.havoc, w)
	t.blocks[fb].instrs = []string{fmt.Sprintf(".havoc %d %d", d, len(t.l.havoc)-1)}
	t.blocks[fb].term = fmt.Sprintf(".jump %d []", next)
	t.cur.term = fmt.Sprintf(".call %d %s %d %d [] %d", fid, list(args), d, next, fb)
	t.cur = t.blocks[next]
}

// switchInstr is a chain of equality tests.
func (t *fnState) switchInstr(in llvm.Value) {
	v := t.val(in.Operand(0))
	w := in.Operand(0).Type().IntTypeWidth()
	n := in.SuccessorsCount()
	for i := 1; i < n; i++ {
		k := in.Operand(2 * i)
		kv := t.fresh()
		t.emit(kv, fmt.Sprintf("(.const %d %s)", w, lint(k.SExtValue())))
		e, c := t.fresh(), t.fresh()
		t.emit(e, "(.icmp .eq)", v, kv)
		t.emit(c, ".cond", e)
		dest := in.Successor(i)
		rest := t.newBlock()
		t.cur.term = fmt.Sprintf(".br %d %d %s %d []", c, t.bbIndex[dest], list(t.edgeArgs(t.curBB, dest)), rest)
		t.cur = t.blocks[rest]
	}
	t.cur.term = t.jumpTo(in.Successor(0))
}
