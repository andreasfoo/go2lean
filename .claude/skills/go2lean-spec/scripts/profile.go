//go:build ignore

// profile prints, for each package of a Go module, what a spec plan needs:
// the API with its documented contracts, the input surface, function pairs
// and normalisers (round trip, idempotence), comparators, signs of a
// standard, concurrency and unsafe use, existing fuzz targets, and the
// files bug fixes touched most. Standard library only; it reads source and
// git history and runs no code of the module.
//
//	go run .claude/skills/go2lean-spec/scripts/profile.go <module-dir>
package main

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	contractRe = regexp.MustCompile(`(?i)\b(returns?|reports whether|produces|converts|must|never|always|guarantee[sd]?|at most|at least|panics?|if .{1,40}(then|,)|otherwise|exactly|non-?empty|nil if|error if|e\.g\.|for example)\b`)
	standardRe = regexp.MustCompile(`(?i)\b(RFC ?\d{3,4}|CommonMark|ISO ?\d{3,5}|IETF|POSIX|Unicode|UTF-8|specification|spec\b|grammar|ECMA-?\d+|IEEE ?\d+)`)
	fixRe      = regexp.MustCompile(`(?i)\b(fix|fixes|fixed|bug|crash|panic|wrong|incorrect)\b`)
	pairs      = [][2]string{{"Encode", "Decode"}, {"Marshal", "Unmarshal"}, {"Parse", "String"}, {"Parse", "Format"},
		{"Quote", "Unquote"}, {"Escape", "Unescape"}, {"Compress", "Decompress"}, {"Encrypt", "Decrypt"},
		{"Serialize", "Deserialize"}, {"Pack", "Unpack"}, {"Encode", "Parse"}, {"Format", "Parse"}}
	normRe    = regexp.MustCompile(`^(Clean|Normali[sz]e|Canonical|Sanitize|Trim|To(Lower|Upper|Snake|Camel|Kebab|Title|Screaming)|Dedup|Unique|Sort|Simplify|Minify)`)
	compareRe = regexp.MustCompile(`^(Compare|Less|Equal|Equals|Cmp|Distance|Similarity|Match|Diff)`)
	stdImport = map[string]bool{"unicode": true, "unicode/utf8": true, "unicode/utf16": true, "net/http": true, "net/url": true,
		"regexp": true, "mime": true, "html": true, "html/template": true, "text/template": true, "strconv": true, "time": true}
)

type fn struct {
	name, sig, doc string
	exported       bool
	contract       bool
	input          bool
	// effect: no results, or a setter. Such functions are checked through
	// what they change, not what they return, so they rank last.
	effect bool
}

type pkg struct {
	dir, name   string
	funcs       []fn
	imports     map[string]bool
	goStmts     int
	chans       int
	unsafe      bool
	fuzz        []string
	standardDoc []string
	lines       int
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run profile.go <module-dir>")
		os.Exit(2)
	}
	root, _ := filepath.Abs(os.Args[1])
	pkgs := map[string]*pkg{}
	fset := token.NewFileSet()
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			n := d.Name()
			if path != root && (strings.HasPrefix(n, ".") || strings.HasPrefix(n, "_") || n == "vendor" || n == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil
		}
		dir := filepath.Dir(path)
		p := pkgs[dir]
		if p == nil {
			p = &pkg{dir: dir, imports: map[string]bool{}}
			pkgs[dir] = p
		}
		isTest := strings.HasSuffix(path, "_test.go")
		if isTest {
			for _, decl := range f.Decls {
				if fd, ok := decl.(*ast.FuncDecl); ok && strings.HasPrefix(fd.Name.Name, "Fuzz") {
					p.fuzz = append(p.fuzz, fd.Name.Name)
				}
			}
			return nil
		}
		p.name = f.Name.Name
		p.lines += fset.Position(f.End()).Line
		for _, im := range f.Imports {
			path := strings.Trim(im.Path.Value, `"`)
			p.imports[path] = true
			if path == "unsafe" {
				p.unsafe = true
			}
		}
		if f.Doc != nil {
			if m := standardRe.FindString(f.Doc.Text()); m != "" {
				p.standardDoc = append(p.standardDoc, m)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n.(type) {
			case *ast.GoStmt:
				p.goStmts++
			case *ast.ChanType:
				p.chans++
			}
			return true
		})
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			name := fd.Name.Name
			if fd.Recv != nil && len(fd.Recv.List) > 0 {
				name = recvName(fd.Recv.List[0].Type) + "." + name
			}
			doc := ""
			if fd.Doc != nil {
				doc = strings.Join(strings.Fields(fd.Doc.Text()), " ")
				if m := standardRe.FindString(doc); m != "" {
					p.standardDoc = append(p.standardDoc, m)
				}
			}
			p.funcs = append(p.funcs, fn{
				name:     name,
				sig:      sigString(fd.Type),
				doc:      doc,
				exported: fd.Name.IsExported(),
				contract: doc != "" && contractRe.MatchString(doc),
				input:    takesInput(fd.Type),
				effect:   fd.Type.Results == nil || strings.HasPrefix(fd.Name.Name, "Set"),
			})
		}
		return nil
	})

	hot := hotspots(root)
	var dirs []string
	for d, p := range pkgs {
		if p.name != "" {
			dirs = append(dirs, d)
		}
	}
	sort.Strings(dirs)
	fmt.Printf("# Profile of %s\n\n%d packages\n", root, len(dirs))
	for _, d := range dirs {
		printPkg(root, pkgs[d], hot)
	}
	if len(hot) > 0 {
		fmt.Printf("\n## Files most touched by fix commits\n\n")
		type kv struct {
			f string
			n int
		}
		var all []kv
		for f, n := range hot {
			all = append(all, kv{f, n})
		}
		sort.Slice(all, func(i, j int) bool { return all[i].n > all[j].n || all[i].n == all[j].n && all[i].f < all[j].f })
		for i, e := range all {
			if i == 15 {
				break
			}
			fmt.Printf("- %s: %d\n", e.f, e.n)
		}
	}
}

func printPkg(root string, p *pkg, hot map[string]int) {
	rel, _ := filepath.Rel(root, p.dir)
	fmt.Printf("\n## %s (package %s, %d lines)\n\n", rel, p.name, p.lines)
	var exported, contracts, inputs []fn
	names := map[string]bool{}
	for _, f := range p.funcs {
		names[short(f.name)] = true
		if f.exported {
			exported = append(exported, f)
			if f.contract {
				contracts = append(contracts, f)
			}
			if f.input && (!f.effect || strings.Contains(f.sig, "http.ResponseWriter")) {
				inputs = append(inputs, f)
			}
		}
	}
	documentedN := 0
	for _, f := range exported {
		if f.doc != "" {
			documentedN++
		}
	}
	fmt.Printf("- exported functions: %d (%d documented, %d contract-like)\n", len(exported), documentedN, len(contracts))
	var std []string
	for im := range p.imports {
		if stdImport[im] || strings.HasPrefix(im, "encoding/") || strings.HasPrefix(im, "compress/") || strings.HasPrefix(im, "crypto/") {
			std = append(std, im)
		}
	}
	sort.Strings(std)
	if len(std) > 0 {
		fmt.Printf("- standard-related imports: %s\n", strings.Join(std, ", "))
	}
	if len(p.standardDoc) > 0 {
		fmt.Printf("- standards named in docs: %s\n", strings.Join(uniq(p.standardDoc), ", "))
	}
	if p.goStmts > 0 || p.chans > 0 || p.imports["sync"] {
		fmt.Printf("- concurrency: %d go statements, %d channel types, sync imported: %v\n", p.goStmts, p.chans, p.imports["sync"])
	}
	if p.unsafe {
		fmt.Printf("- imports unsafe\n")
	}
	if len(p.fuzz) > 0 {
		fmt.Printf("- existing fuzz targets: %s\n", strings.Join(p.fuzz, ", "))
	}
	var pr []string
	for _, q := range pairs {
		for n := range names {
			if strings.HasPrefix(n, q[0]) {
				other := q[1] + strings.TrimPrefix(n, q[0])
				if names[other] {
					pr = append(pr, n+" / "+other)
				}
			}
		}
	}
	if len(pr) > 0 {
		sort.Strings(pr)
		fmt.Printf("- pairs (round trip): %s\n", strings.Join(uniq(pr), "; "))
	}
	var norm, cmp []string
	for _, f := range exported {
		s := short(f.name)
		if normRe.MatchString(s) {
			norm = append(norm, f.name)
		}
		if compareRe.MatchString(s) {
			cmp = append(cmp, f.name)
		}
	}
	if len(norm) > 0 {
		fmt.Printf("- normalisers (idempotence): %s\n", strings.Join(norm, ", "))
	}
	if len(cmp) > 0 {
		fmt.Printf("- comparators and distances (symmetry, reflexivity): %s\n", strings.Join(cmp, ", "))
	}
	if len(inputs) > 0 {
		fmt.Printf("\n### Input surface (exported, with a result, taking []byte, string or io.Reader; HTTP handlers)\n\n")
		for _, f := range inputs {
			fmt.Printf("- `%s%s`\n", f.name, f.sig)
		}
	}
	// Every documented exported function is a candidate for a doc-driven
	// spec; * marks doc comments that read like a contract (returns, must,
	// never, at most, ...).
	var documented []fn
	effects := 0
	for _, f := range exported {
		switch {
		case f.doc == "":
		case f.effect:
			effects++
		default:
			documented = append(documented, f)
		}
	}
	// Contract-like functions that take arguments first, accessors with
	// no arguments after them.
	rankOf := func(f fn) int {
		r := 0
		if f.contract {
			r += 2
		}
		if !strings.HasPrefix(f.sig, "()") {
			r++
		}
		return r
	}
	sort.SliceStable(documented, func(i, j int) bool {
		return rankOf(documented[i]) > rankOf(documented[j])
	})
	if len(documented) > 0 {
		fmt.Printf("\n### Documented API, contract-like (*) first; %d setters and no-result functions left out\n\n", effects)
		for i, f := range documented {
			if i == 60 {
				fmt.Printf("- ... %d more\n", len(documented)-60)
				break
			}
			mark := " "
			if f.contract {
				mark = "*"
			}
			fmt.Printf("- %s `%s%s`: %s\n", mark, f.name, f.sig, firstSentence(f.doc, 200))
		}
	}
	var fileHot []string
	for f, n := range hot {
		if filepath.Dir(f) == rel || rel == "." && !strings.Contains(f, "/") {
			fileHot = append(fileHot, fmt.Sprintf("%s (%d)", filepath.Base(f), n))
		}
	}
	if len(fileHot) > 0 {
		sort.Strings(fileHot)
		fmt.Printf("\n- fix-commit touches: %s\n", strings.Join(fileHot, ", "))
	}
}

// hotspots counts, per non-test Go file, the commits whose subject looks
// like a fix.
func hotspots(root string) map[string]int {
	out, err := exec.Command("git", "-C", root, "log", "--no-merges", "--format=@@%s", "--name-only").Output()
	if err != nil {
		return nil
	}
	hot := map[string]int{}
	fix := false
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		l := sc.Text()
		if strings.HasPrefix(l, "@@") {
			fix = fixRe.MatchString(l)
			continue
		}
		if fix && strings.HasSuffix(l, ".go") && !strings.HasSuffix(l, "_test.go") {
			if _, err := os.Stat(filepath.Join(root, l)); err == nil {
				hot[l]++
			}
		}
	}
	return hot
}

func takesInput(t *ast.FuncType) bool {
	if t.Params == nil {
		return false
	}
	for _, f := range t.Params.List {
		switch s := exprString(f.Type); s {
		case "[]byte", "string", "io.Reader", "*http.Request", "http.ResponseWriter", "[]rune":
			return true
		}
	}
	return false
}

func sigString(t *ast.FuncType) string {
	var ps []string
	if t.Params != nil {
		for _, f := range t.Params.List {
			ts := exprString(f.Type)
			if len(f.Names) == 0 {
				ps = append(ps, ts)
			}
			for _, n := range f.Names {
				ps = append(ps, n.Name+" "+ts)
			}
		}
	}
	s := "(" + strings.Join(ps, ", ") + ")"
	if t.Results != nil {
		var rs []string
		for _, f := range t.Results.List {
			n := len(f.Names)
			if n == 0 {
				n = 1
			}
			for i := 0; i < n; i++ {
				rs = append(rs, exprString(f.Type))
			}
		}
		if len(rs) == 1 {
			s += " " + rs[0]
		} else {
			s += " (" + strings.Join(rs, ", ") + ")"
		}
	}
	return s
}

func exprString(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.StarExpr:
		return "*" + exprString(x.X)
	case *ast.SelectorExpr:
		return exprString(x.X) + "." + x.Sel.Name
	case *ast.ArrayType:
		if x.Len == nil {
			return "[]" + exprString(x.Elt)
		}
		return "[...]" + exprString(x.Elt)
	case *ast.MapType:
		return "map[" + exprString(x.Key) + "]" + exprString(x.Value)
	case *ast.Ellipsis:
		return "..." + exprString(x.Elt)
	case *ast.FuncType:
		return "func" + sigString(x)
	case *ast.InterfaceType:
		return "interface{...}"
	case *ast.ChanType:
		return "chan " + exprString(x.Value)
	case *ast.IndexExpr:
		return exprString(x.X) + "[" + exprString(x.Index) + "]"
	}
	return "?"
}

func recvName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.StarExpr:
		return recvName(x.X)
	case *ast.Ident:
		return x.Name
	case *ast.IndexExpr:
		return recvName(x.X)
	case *ast.IndexListExpr:
		return recvName(x.X)
	}
	return "?"
}

// firstSentence is the doc comment up to its first full stop, at most n
// bytes.
func firstSentence(doc string, n int) string {
	if i := strings.Index(doc, ". "); i >= 0 {
		doc = doc[:i+1]
	}
	if len(doc) > n {
		doc = doc[:n] + "..."
	}
	return doc
}

func short(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}

func uniq(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
