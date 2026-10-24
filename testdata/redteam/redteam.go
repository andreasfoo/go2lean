// Package redteam is the target of the proof judge's red-team corpus
// (scripts/redteam.sh): every goal in attacks/ claims something false of
// Max2 and tries to get it accepted anyway; control.json is a true goal
// with a real proof, which must still be accepted.
package redteam

// Max2 returns the larger of a and b.
func Max2(a, b int) int {
	if a > b {
		return a
	}
	return b
}
