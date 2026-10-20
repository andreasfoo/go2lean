package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"time"
)

// startMemProfile writes a heap profile to $GO2LEAN_MEMPROFILE whenever
// the heap in use passes its previous peak by a tenth, so the file holds
// the allocations at (about) the run's peak.
func startMemProfile() {
	path := os.Getenv("GO2LEAN_MEMPROFILE")
	if path == "" {
		return
	}
	go func() {
		var peak uint64
		var ms runtime.MemStats
		for range time.Tick(2 * time.Second) {
			runtime.ReadMemStats(&ms)
			if ms.HeapInuse < peak+peak/10 {
				continue
			}
			peak = ms.HeapInuse
			if f, err := os.Create(path); err == nil {
				pprof.WriteHeapProfile(f)
				f.Close()
			}
		}
	}()
}

// limitMemory sets a soft memory limit of a third of physical memory when
// GOMEMLIMIT is not set. Loading and translating a large module keeps
// about 2.5 GB live, and the collector's default headroom let the process
// grow to three times that (7.5 GB for tingly-box's protocol packages,
// which got runs killed next to their searches); with the limit it stays
// near 4 GB, at the cost of more collection.
func limitMemory() {
	if os.Getenv("GOMEMLIMIT") != "" {
		return
	}
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return
	}
	var kb int64
	if _, err := fmt.Sscanf(string(b), "MemTotal: %d kB", &kb); err != nil || kb <= 0 {
		return
	}
	debug.SetMemoryLimit(kb * 1024 / 3)
}
