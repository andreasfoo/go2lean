package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// CheckComparator tests the sandbox features used by the pinned Lean
// comparator. A successful ordinary bwrap probe is insufficient: older
// bubblewrap versions lack --clearenv. No Lean or target code is run.
// COMPARATOR_BWRAP follows Lake's executable override.
func CheckComparator() error {
	bin := os.Getenv("COMPARATOR_BWRAP")
	if bin == "" {
		bin = "bwrap"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	args := []string{"--ro-bind", "/", "/", "--tmpfs", "/home", "--tmpfs", "/root", "--tmpfs", "/run/user", "--tmpfs", "/tmp", "--dir", "/tmp/home", "--dev", "/dev", "--proc", "/proc", "--clearenv", "--setenv", "HOME", "/tmp/home", "--unshare-all", "--die-with-parent", "--new-session", "--", "/bin/true"}
	out, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("Lean comparator sandbox (%s): %v: %s; install bubblewrap supporting --clearenv and usable user namespaces", bin, err, strings.TrimSpace(string(out)))
	}
	return nil
}
