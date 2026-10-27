package sandbox

import (
	"fmt"
	"os"
	"testing"
)

// Local development may lack user namespaces. CI and release validation must
// not mistake skipped integration tests for evidence of working isolation.
func TestMain(m *testing.M) {
	if os.Getenv("GO2LEAN_REQUIRE_SANDBOX_TESTS") == "1" {
		if Mode() != ModeBwrap || os.Getenv("GO2LEAN_SANDBOX") != "" || Unconfined() {
			fmt.Fprintln(os.Stderr, "sandbox integration gate requires working bwrap/prlimit and no isolation opt-outs")
			os.Exit(1)
		}
		if _, err := Confine(nil, nil, ""); err != nil {
			fmt.Fprintln(os.Stderr, "sandbox integration gate:", err)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}
