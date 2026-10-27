package pipeline

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"

	"github.com/andreasfoo/go2lean/internal/builtin"
	"github.com/andreasfoo/go2lean/internal/contract"
	"github.com/andreasfoo/go2lean/internal/ir"
)

func validateCompareOrderOverlay(env Env) error {
	b := env.CompareOrder
	if b == nil {
		return nil
	}
	if err := b.Validate(); err != nil {
		return err
	}
	if src, ok := env.Overlay[b.File]; !ok || !bytes.Equal(src, []byte(b.Source)) {
		return fmt.Errorf("compare-order: compiler overlay does not contain the canonical bound source")
	}
	for path := range env.Overlay {
		if path != b.File && filepath.Clean(path) == b.File {
			return fmt.Errorf("compare-order: ambiguous generated overlay path %s", path)
		}
	}
	return nil
}

func assessCompareOrder(w contract.Workspace, env Env, progs []*ir.Prog, out io.Writer) error {
	if env.CompareOrder == nil {
		return nil
	}
	c := builtin.AssessCompareOrder(env.CompareOrder, progs)
	path, err := builtin.WriteAssessment(w, env.CompareOrder, c)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "compare-order: %s (%d required functions, %d call fallback sites); assessment: %s\n",
		c.Status, len(c.Functions), c.CallFallbacks, path)
	for _, issue := range c.Issues {
		fmt.Fprintf(out, "  %s.%s block %d: %s: %s\n",
			issue.Package, issue.Function, issue.Block, issue.Code, issue.Reason)
	}
	if c.Status != "static-model-eligible" {
		return fmt.Errorf("compare-order: required call graph is unsupported; see %s", path)
	}
	return nil
}
