package poc

import (
	"strings"
	"testing"
)

// The replay controller's own map accesses race (its lock is hidden from
// the detector): such reports are not the program's.
func TestRaceReportSkipsController(t *testing.T) {
	controller := `==================
WARNING: DATA RACE
Read at 0x00c000282000 by goroutine 9:
  runtime.mapaccess1_fast64()
      /go/src/internal/runtime/maps/runtime_fast64.go:196 +0x0
  example.com/m.go2leanGate()
      /m/zz_go2lean_replay.go:133 +0x12c

Previous write at 0x00c000282000 by goroutine 8:
  runtime.mapassign_fast64()
      /go/src/internal/runtime/maps/runtime_fast64.go:20 +0x0
  example.com/m.go2leanDrive()
      /m/zz_go2lean_replay.go:250 +0x1
==================
`
	program := `==================
WARNING: DATA RACE
Write at 0x00c00009a008 by goroutine 10:
  example.com/m.PeekEarly.func1()
      /m/races.go:81 +0x3c

Previous read at 0x00c00009a008 by goroutine 9:
  example.com/m.PeekEarly()
      /m/races.go:85 +0x1
==================
`
	if got := raceReport(controller); got != "" {
		t.Fatalf("controller race reported: %s", got)
	}
	if got := raceReport(controller + program); !strings.Contains(got, "PeekEarly.func1") {
		t.Fatalf("program race not reported: %q", got)
	}
}
