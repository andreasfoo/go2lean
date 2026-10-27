// Package leanassets contains the trusted Lean runtime shipped with go2lean.
// Embedding the sources keeps installed binaries independent of a checkout;
// Lake's build products are generated in the user's content-addressed cache.
package leanassets

import "embed"

// Core contains only the core library, not generated models or example proofs.
//
//go:embed lakefile.toml lean-toolchain lake-manifest.json Go2Lean.lean Go2Lean/*.lean Go2Lean/SSA/*.lean Go2Lean/Dialect/*.lean Go2Lean/Proof/*.lean
var Core embed.FS
