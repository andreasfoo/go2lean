# github.com/gobwas/glob: go2lean report, round 2

## 1. Confirmed bugs

### 1.1 An unmatched `}` compiles as a literal, but the grammar says it is an error

- **Property:** `SpecCompileBraceBalance`.
- **Input:** `Compile("unexpected `}`")`. This is a string the engine mined from the source. Its only `}` has no matching `{`. Separators were empty.
- **Real run:** it returned a `*Pattern` (printed as its own text) and a nil error. The model run did the same.
- **Where the source disagrees:**
  - The grammar in `Compile`'s doc says `c matches character c (c != ..., `{`, `}`)`.
  - The parser has a dedicated error path for this case, `Reason: "unexpected `}`"` in the `syntax.TermsClose` branch of `compile`. It never fires for a `}` outside a group: the lexer evidently emits that byte as `Text`, so the parser never sees a `TermsClose`.
  - The `syntax` lexer was not included in the prompt, so I cannot point at its exact line.
- **Fix:** pick one of two.
  - Make the lexer emit `TermsClose` for a `}` outside a group, so the existing parser check reports it.
  - Or, if accepting it is meant to stay for compatibility, change the grammar doc to "`}` is literal outside a group". `QuoteMeta` keeps escaping it either way.

### 1.2 `*` treats an invalid separator rune as U+FFFD, but `?` does not

- **Property:** `SeqStarSeparators`.
- **Go fuzzer, real code:** it failed on `s = "\xe7"`, with separators decoded from the bytes `"0000"`. That is most likely the single rune 0x30303030, which is outside the Unicode range.
- **Where it goes wrong:** `compile` builds `sepStr = string(sep)`.
  - Converting an invalid rune (negative, above 0x10FFFF, or a surrogate) to a string yields `"�"`.
  - `starMatcher.reach`, `storeSkip` and the shaped matchers (`noSep`) use that string, through `strings.IndexAny` / `ContainsAny`. Both functions read an invalid byte in the input as RuneError, so the star stops at it.
  - `charMatcher.Match` and the non-terminal star compare the raw `[]rune` with `slices.Contains`. That never equals the bad rune.
- **Smallest witness (traced in the source, not run):**
  - `Compile("*", -1).Match("�")` returns false, because the terminal star's reach is 0.
  - `Compile("*?", -1).Match("�")` returns true, as does `Compile("?", -1)`.
  - So `*` rejects a string that `*?` accepts, even though `*?` should only ever match a subset of what `*` matches.
- **Fix (in `compile`):** drop the invalid runes before both uses, e.g. `sepCopy = slices.DeleteFunc(slices.Clone(sep), func(r rune) bool { return !utf8.ValidRune(r) })` followed by `sepStr = string(sepCopy)`.
  - `utf8.DecodeRune` never returns an invalid rune, so this changes nothing for valid separators.
  - `Separators()` still returns `sep` as it was given.

### 1.3 `SeqAlternativesExact` fails on real code

- **Random search, seed 42:** 6 alternatives. The model panicked; the real run returned false.
- **Go fuzzer:** it failed on `t = ""` with the alternatives decoded from `"\x9a\x80,0000"`.
- I cannot decode that input exactly. Two causes in the source fit it:
  - **(a) `commonSuffix` treats any two invalid bytes as equal. This is a confirmed bug, traced in the source.**
    - The check is `if ra != rb || wa != wb { break }`. Every invalid byte decodes as `(RuneError, 1)`, so `commonSuffix("\x80", "\x81")` returns `"\x80"`.
    - `requiredSuffix` then makes `"\x80"` the pattern's required suffix.
    - The precondition `!strings.HasSuffix(s, p.suffix)` in `Pattern.Match` then rejects `"\x81"`, so `Compile("{\x80,\x81}").Match("\x81")` returns false.
    - The pattern compiles: `SpecQuoteMetaRoundTrip` held for 30 s of fuzzing on arbitrary bytes, so the lexer keeps raw bytes.
    - **Fix:** compare bytes, not decoded runes: `if wa != wb || a[len(a)-i-wa:len(a)-i] != b[len(b)-i-wb:len(b)-i] { break }`.
    - The exhaustive `SpecCommonSuffixIsCommonSuffix` missed this because its domain (`""`, `"a"` and mined literals) has no two distinct invalid bytes.
  - **(b) Possible: `QuoteMeta` may not escape `,`.**
    - `syntax.IsSpecial` is not in the prompt. `SpecQuoteMetaUnescapes` does not require `,` to be escaped.
    - `SeqMatchAgreesWithReference` already strips commas from group texts, which suggests the spec author knew.
    - If so, an alternative containing a comma splits into two inside `{...}`. That breaks the promise that `QuoteMeta` escapes "all glob meta characters".
    - **Fix:** have `IsSpecial` also return true for `,`. `\,` is valid everywhere, since the grammar says `` `\` c matches character c``.
  - The revised specs keep the two apart (section 4): `SeqQuotedGroupMatchesItself` tests (b), and `SeqAlternativesExact` now strips commas, so only (a) can still fail it.

### 1.4 `SeqMatchAgreesWithReference` fails on real code

- **Go fuzzer:** it failed on `s = "0"` plus a long kinds/texts/separators blob. The random-search counterexample was spurious: the model panicked, while the real run returned true.
- Two causes, one in the spec and one in the package:
  - **Spec glitch.** A kind-0 term with empty text adds nothing to the pattern. `*`, then `""`, then `*` therefore becomes `**`, while the reference keeps two separator-limited stars. With `'0'` as a separator and `s = "0"`, the two disagree. This is fixed in the revision.
  - **Package bug: the `**` reset discards a restart point that is still needed (traced in the source, not run).**
    - `MustCompile("*{abc,b}*c??").Match("abcXc")` returns false. The right answer is true: `*`=`a`, `{b}`, `*`=`""`, `c`, `?`=`X`, `?`=`c`. There are no separators, so both stars are separator-free.
    - **Trace:**
      - The first star saves restart point R at offset 1.
      - The group saves its "try `b`" checkpoint at offset 0 and matches `abc`.
      - The second star, at offset 3, finds `c` at offset 4 in `storeSkip`. It calls `x.storeStar(1+j, len(m.Sep) == 0)`.
      - In `storeStar`, `x.state.stars = x.state.stars[:x.starsFloor]` runs with the root's `starsFloor` of 0, and R is dropped.
      - After that, only offsets 3 and 4 are tried for `c`, and the group checkpoint tries `b` at offset 0. Every path fails.
    - **Why the reset is wrong here:** it assumes that resuming an earlier star only moves the walk forward. A group breaks that assumption. Resuming R and taking the shorter alternative `b` reaches the second star at offset 2, which is earlier than 3. `starsFloor` only protects restart points across the inside of a group, not across the terms that come after it.
    - **Fix:** keep the reset only where it is sound. Have `annotateStars` mark every star that follows an `altMatcher` in walk order, for example with a `NoReset` field. Then pass `len(m.Sep) == 0 && !m.NoReset` at the two `storeStar` call sites (`storeSkip` and `starMatcher.Match`).
      - Raising `starsFloor` after the group in `multiMatcher.Match` is not enough on its own. A resumed checkpoint re-enters below the group, so the raised floor would be lost.
    - `SeqStarAltStar`, new in the revision, covers this family of patterns.

### 1.5 Panics that are real but not bugs (internal preconditions)

Each of these really panicked, but on a state that `compile` and `Pattern.Match` never build. No fix is needed. The harness should reach these functions only through `Compile` and `Match`, or state the precondition.

| Function | Real panic | Precondition the package keeps |
|---|---|---|
| `altMatcher.Match/String`, `multiMatcher.Match/String` | nil dereference: nil children; for `altMatcher.Match`, also a nil `x.state` | children are non-nil; an alt always runs with state (`needsState`) |
| `frame.index`, `matchContext.branch` | index out of range [-1] | `depth >= 0` |
| `matchContext.storeStar`, `starMatcher.Match` | slice bounds out of range | `0 <= starsFloor <= len(stars)`; offsets are inside the input |
| `matchState.allocPath` | slice bounds (n < 0) | `n >= 0` |
| `starMatcher.storeSkip` | `s[1:0]` | only called with `len(s) > 0` and `Next != ""` (see `starMatcher.Match`) |

## 2. Spurious counterexamples

- **`Compile`, `compile` and `MustCompile` no-panic.** The model hit `slice_bounds` with out-of-range separator runes, but the real run was fine. This is a modelling artifact of `string(sep)` on invalid runes. The property itself is right.
  - The no-panic property for `MustCompile` is too strong by design: `MustCompile("{")` must panic. Restate it as "does not panic when `Compile(pattern, separators...)` returns a nil error".
- **`SpecCompileLiteralMatchesExactly`** (`"="`), **`SpecMustCompileAgreesWithCompile`**, **`SpecQuoteMetaRoundTrip`** and **`SpecQuoteMetaUnescapes`.** The oracle holds in the real run. In the QuoteMeta cases the model chose its own results for the unmodelled `internal/bytealg.IndexByte`. These specs are right.
- **`SeqStarSeparators` and `SeqMatchAgreesWithReference`, random seed 42.** The model panicked; the real run returned true. Both specs did fail under the fuzzer, though, for the reasons in 1.2 and 1.4.
- **`SpecCompileBraceBalance`.** This was not spurious (see 1.1). I split it so that its other half, unclosed `{`, gets checked on its own.
- **`SeqMatchAgreesWithReference`.** The empty-literal glitch made it too strong (1.4). It is fixed in the revision.

## 3. Properties that held

**Certified by the exhaustive engine** (`exhausted`): the domain was boundary values, mined literals, |v| <= 8, slices up to length 3, and strings `""` / `"a"`.
- **Postconditions:**
  - `SpecSyntaxErrorErrorFormat`: 3159 inputs. The 1 mutant fails it, so it has teeth.
  - `SpecCommonSuffixIsCommonSuffix`: 1225 inputs. 8 of 12 mutants fail it.
    - Caveat: this domain has no invalid UTF-8, which is exactly where the 1.3(a) bug lives. Read it as "correct on valid UTF-8", not as a proof.
- **No panic, on ordinary value types:**
  - `SyntaxError.Error` (588 inputs)
  - `commonSuffix` (1225)
  - `noSep` (1764)
  - `runeRangeMatcher.String` (2048)
  - `textMatcher.String` (1071)
  - `containsMatcher.String` (1250)
  - `voidMatcher.String` (1)
- **No panic, but on only 1-4 inputs:** `annotateStars`, `foldTail`, `leadingLiteral`, `minLength`, `needsNormalize`, `needsState`, `normalizeSequence`, `requiredSuffix`, `simplify`, `specialize`.
  - Their argument is the `matcher` interface, so the domain held little more than nil and trivial trees. This certifies almost nothing.
- **Certified modulo havoc values for unmodelled calls** (`exhausted_havoc`): `charMatcher.String`, `formatCheckpoint`, `formatRunes`, `runeSetMatcher.String`.
- **Certified under an under-approximation** (`exhausted_assuming`): `QuoteMeta`, `acquireState` and `parseRange`.
  - `parseRange` got one input, so the class lexer was effectively not explored.

**Only "no counterexample found" (random search):**
- **No panic:**
  - `Pattern.String`, `Pattern.Separators`, `formatStack`, `releaseState`, `resetCheckpoints`
  - `matchContext.next/push`, `starMatcher.reach/String`
  - `Match` of the char, contains, prefix, prefix-suffix, suffix, text, range, set and void matchers
  - `String` of the prefix, prefix-suffix and suffix matchers
- **Postconditions:**
  - `SpecPatternStringIsSource` and `SpecPatternSeparatorsKept`: trivial getters, fine as they are.
  - `SpecCompileLiteralAccepted` and `SpecCompileResultShape`: the mutation check below shows they check nothing.

**Held under the Go fuzzer for about 30 s:** `SpecQuoteMetaRoundTrip`, `SpecQuoteMetaUnescapes`, `SpecSyntaxErrorErrorFormat`.

**Specs that no mutant fails:** `SpecCompileLiteralAccepted` and `SpecCompileResultShape` (0 of 24).
- They check only `String()`, `Separators()` and the shape of the error. `compile` copies those through, whatever the matcher tree looks like.
- `LiteralAccepted` also skips every pattern that contains a brace. Two of the survivors (`eq -> ne` in compile blocks 14 and 24) are most likely the `i == len(stack)` checks for the `{,a}` and `{a,}` cases, which only braces reach.
- The `branch negated` survivor in block 0 is probably `if debug.Enabled`, which is an equivalent mutant.
- **Restated:**
  - `SpecCompileLiteralAccepted` now also requires the compiled pattern to match exactly its own text: not one byte more, less or different. `SpecCompileLiteralMatchesExactly` is merged into it.
  - `SpecCompileResultShape` now also checks matching for patterns made only of literals and groups (valid UTF-8, balanced braces). The pattern must match every string in its brace expansion, and the expansion decides each neighbour formed by adding a `\x01` or dropping the last byte.

## 4. Revised spec file

Changes:
- `SpecCompileLiteralAccepted` and `SpecCompileResultShape` now constrain matching (section 3). `SpecCompileLiteralMatchesExactly` is merged into the first.
- `SpecCompileBraceBalance` is split in two:
  - `SpecCompileUnclosedBrace` should hold.
  - `SpecCompileStrayCloseBrace` encodes the documented grammar and fails until 1.1 is decided.
- `SeqAlternativesExact` now strips commas, so it isolates 1.3(a).
- New `SeqQuotedGroupMatchesItself` isolates 1.3(b).
- `SeqMatchAgreesWithReference` no longer lets an empty literal fuse two stars.
- New `SeqStarAltStar` covers the reset bug in 1.4.
- `SeqStarSeparators` is unchanged; it is the regression test for 1.2.
- All other specs are unchanged.

```go
package glob

// go2lean:spec Compile
// SpecCompileLiteralAccepted: a pattern made only of plain characters (none of
// `*`, `?`, `\`, `[`, `{`, `}`; the grammar lets every other character, `,`
// and `]` included, stand for itself) compiles, String returns it, and it
// matches exactly its own text: not a byte more, less or different.
func SpecCompileLiteralAccepted(pattern string, separators []rune, ret0 *Pattern, ret1 error) bool {
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*', '?', '\\', '[', '{', '}':
			return true
		}
	}
	if ret1 != nil || ret0 == nil {
		return false
	}
	if ret0.String() != pattern {
		return false
	}
	cands := []string{pattern, pattern + "x", "x" + pattern, ""}
	if len(pattern) > 0 {
		cands = append(cands,
			pattern[1:],
			pattern[:len(pattern)-1],
			pattern[:len(pattern)-1]+"\x01",
		)
	}
	for _, t := range cands {
		if ret0.Match(t) != (t == pattern) {
			return false
		}
	}
	return true
}

// go2lean:spec Compile
// SpecCompileResultShape: exactly one of the results is set; an error is a
// *SyntaxError whose Offset is a byte offset inside the pattern; a compiled
// pattern reports its source text and the separators it was given. Beyond
// the shape: a valid UTF-8 pattern made of literals and balanced groups only
// matches every string of its brace expansion, and the expansion decides
// the neighbours of those strings.
func SpecCompileResultShape(pattern string, separators []rune, ret0 *Pattern, ret1 error) bool {
	if ret1 != nil {
		if ret0 != nil {
			return false
		}
		se, ok := ret1.(*SyntaxError)
		if !ok || se == nil {
			return false
		}
		return se.Offset >= 0 && se.Offset <= len(pattern)
	}
	if ret0 == nil {
		return false
	}
	if ret0.String() != pattern {
		return false
	}
	got := ret0.Separators()
	if len(got) != len(separators) {
		return false
	}
	for i := range got {
		if got[i] != separators[i] {
			return false
		}
	}

	if string([]rune(pattern)) != pattern {
		return true
	}
	depth := 0
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*', '?', '\\', '[':
			return true
		case '{':
			depth++
		case '}':
			depth--
			if depth < 0 {
				return true
			}
		}
	}
	if depth != 0 {
		return true
	}
	var group func(i int) ([]string, int)
	var seq func(i int, top bool) ([]string, int)
	seq = func(i int, top bool) ([]string, int) {
		out := []string{""}
		for i < len(pattern) {
			c := pattern[i]
			if !top && (c == ',' || c == '}') {
				return out, i
			}
			if c == '{' {
				sub, j := group(i + 1)
				if j < 0 {
					return nil, -1
				}
				var next []string
				for _, x := range out {
					for _, y := range sub {
						next = append(next, x+y)
					}
				}
				if len(next) > 64 {
					return nil, -1
				}
				out = next
				i = j + 1
				continue
			}
			for k := range out {
				out[k] += pattern[i : i+1]
			}
			i++
		}
		return out, i
	}
	group = func(i int) ([]string, int) {
		var all []string
		for {
			s, j := seq(i, false)
			if j < 0 || j >= len(pattern) {
				return nil, -1
			}
			all = append(all, s...)
			if len(all) > 64 {
				return nil, -1
			}
			if pattern[j] == '}' {
				return all, j
			}
			i = j + 1
		}
	}
	set, end := seq(0, true)
	if end < 0 {
		return true
	}
	inSet := func(t string) bool {
		for _, e := range set {
			if e == t {
				return true
			}
		}
		return false
	}
	for _, e := range set {
		if !ret0.Match(e) {
			return false
		}
		if ret0.Match(e+"\x01") != inSet(e+"\x01") {
			return false
		}
		if ret0.Match("\x01"+e) != inSet("\x01"+e) {
			return false
		}
		if len(e) > 0 && ret0.Match(e[:len(e)-1]) != inSet(e[:len(e)-1]) {
			return false
		}
	}
	return true
}

// go2lean:spec Compile
// SpecCompileUnclosedBrace: for a pattern without escapes and classes in
// which no `}` comes before its `{`, Compile fails exactly when a `{` is left
// unclosed; `{}`, `{a,}`, top-level `,`, `*`, `**` and `?` are all valid.
func SpecCompileUnclosedBrace(pattern string, separators []rune, ret0 *Pattern, ret1 error) bool {
	depth := 0
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\', '[':
			return true
		case '{':
			depth++
		case '}':
			depth--
			if depth < 0 {
				return true
			}
		}
	}
	if depth != 0 {
		return ret1 != nil && ret0 == nil
	}
	return ret1 == nil && ret0 != nil
}

// go2lean:spec Compile
// SpecCompileStrayCloseBrace: the documented grammar does not let `}` stand
// for itself, so a `}` without its `{` (outside escapes and classes) is a
// syntax error.
func SpecCompileStrayCloseBrace(pattern string, separators []rune, ret0 *Pattern, ret1 error) bool {
	depth := 0
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\', '[':
			return true
		case '{':
			depth++
		case '}':
			depth--
			if depth < 0 {
				return ret1 != nil && ret0 == nil
			}
		}
	}
	return true
}

// go2lean:spec MustCompile
// SpecMustCompileAgreesWithCompile: MustCompile returns a pattern equivalent to
// the one Compile builds from the same input.
func SpecMustCompileAgreesWithCompile(pattern string, separators []rune, ret *Pattern) bool {
	q, err := Compile(pattern, separators...)
	if err != nil || q == nil || ret == nil {
		return false
	}
	if ret.String() != pattern || q.String() != pattern {
		return false
	}
	if len(ret.Separators()) != len(separators) {
		return false
	}
	return ret.Match(pattern) == q.Match(pattern)
}

// go2lean:spec Pattern.String
// SpecPatternStringIsSource: String returns the source text kept in the pattern.
func SpecPatternStringIsSource(p *Pattern, ret string) bool {
	if p == nil {
		return true
	}
	return ret == p.str
}

// go2lean:spec Pattern.Separators
// SpecPatternSeparatorsKept: Separators returns the separators kept in the
// pattern, all of them, in order.
func SpecPatternSeparatorsKept(p *Pattern, ret []rune) bool {
	if p == nil {
		return true
	}
	if len(ret) != len(p.sep) {
		return false
	}
	for i := range ret {
		if ret[i] != p.sep[i] {
			return false
		}
	}
	return true
}

// go2lean:spec SyntaxError.Error
// SpecSyntaxErrorErrorFormat: the message carries the offset in decimal and the
// reason, in the documented format.
func SpecSyntaxErrorErrorFormat(s *SyntaxError, ret string) bool {
	if s == nil {
		return true
	}
	n := s.Offset
	digits := ""
	if n == 0 {
		digits = "0"
	}
	neg := n < 0
	for n != 0 {
		d := n % 10
		if d < 0 {
			d = -d
		}
		digits = string(rune('0'+d)) + digits
		n /= 10
	}
	if neg {
		digits = "-" + digits
	}
	return ret == "glob: syntax error at "+digits+": "+s.Reason
}

// go2lean:spec QuoteMeta
// SpecQuoteMetaUnescapes: removing the backslash escapes from the result gives
// back s; every glob meta character of the grammar is escaped; a string of
// letters and digits is returned unchanged.
func SpecQuoteMetaUnescapes(s string, ret string) bool {
	out := ""
	for i := 0; i < len(ret); i++ {
		c := ret[i]
		if c == '\\' {
			if i+1 >= len(ret) {
				return false
			}
			i++
			out += ret[i : i+1]
			continue
		}
		switch c {
		case '*', '?', '[', '{', '}':
			// An unescaped meta character.
			return false
		}
		out += ret[i : i+1]
	}
	if out != s {
		return false
	}
	plain := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
			plain = false
		}
	}
	return !plain || ret == s
}

// go2lean:spec QuoteMeta
// SpecQuoteMetaRoundTrip: the quoted string compiles into a pattern that
// matches s and nothing longer or shorter.
func SpecQuoteMetaRoundTrip(s string, ret string) bool {
	valid := string([]rune(s)) == s
	p, err := Compile(ret)
	if err != nil || p == nil {
		return !valid
	}
	if p.String() != ret {
		return false
	}
	if !p.Match(s) {
		return false
	}
	if p.Match(s + "x") {
		return false
	}
	if len(s) > 0 && p.Match(s[:len(s)-1]) {
		return false
	}
	return true
}

// go2lean:spec commonSuffix
// SpecCommonSuffixIsCommonSuffix: the result is a suffix of both strings, and
// it can not be extended by one more equal ASCII byte.
func SpecCommonSuffixIsCommonSuffix(a string, b string, ret string) bool {
	if len(ret) > len(a) || len(ret) > len(b) {
		return false
	}
	if a[len(a)-len(ret):] != ret || b[len(b)-len(ret):] != ret {
		return false
	}
	if len(ret) < len(a) && len(ret) < len(b) {
		ca := a[len(a)-len(ret)-1]
		cb := b[len(b)-len(ret)-1]
		if ca == cb && ca < 0x80 {
			return false
		}
	}
	return true
}

// go2lean:sequence Compile
// SeqAlternativesExact: a group of quoted literal alternatives,
// `{q(a0),q(a1),...}`, matches every alternative and exactly the strings that
// equal one of them. Commas are removed from the alternatives first (whether
// QuoteMeta output is safe inside a group is SeqQuotedGroupMatchesItself's
// business); invalid UTF-8 bytes are kept.
func SeqAlternativesExact(alts []string, t string) bool {
	var as []string
	for _, a := range alts {
		c := ""
		for j := 0; j < len(a); j++ {
			if a[j] != ',' {
				c += a[j : j+1]
			}
		}
		as = append(as, c)
	}
	if len(as) == 0 {
		as = []string{""}
	}
	valid := true
	pat := "{"
	for i, a := range as {
		if i > 0 {
			pat += ","
		}
		pat += QuoteMeta(a)
		if string([]rune(a)) != a {
			valid = false
		}
	}
	pat += "}"
	p, err := Compile(pat)
	if err != nil || p == nil {
		return !valid
	}
	want := false
	for _, a := range as {
		if !p.Match(a) {
			return false
		}
		if a == t {
			want = true
		}
	}
	return p.Match(t) == want
}

// go2lean:sequence Compile
// SeqQuotedGroupMatchesItself: QuoteMeta output is a literal inside a group
// too: `{q(a)}` matches a, and `{q(a),}` matches a and the empty string.
func SeqQuotedGroupMatchesItself(a string) bool {
	if string([]rune(a)) != a {
		return true
	}
	p, err := Compile("{" + QuoteMeta(a) + "}")
	if err != nil || p == nil {
		return false
	}
	if !p.Match(a) {
		return false
	}
	q, err := Compile("{" + QuoteMeta(a) + ",}")
	if err != nil || q == nil {
		return false
	}
	return q.Match(a) && q.Match("")
}

// go2lean:sequence Compile
// SeqStarSeparators: with the given separators, `*` matches exactly the
// strings with no separator character, `?` exactly one non-separator
// character, `*?` and `?*` one or more non-separator characters, and `**`
// anything.
func SeqStarSeparators(sep []rune, s string) bool {
	inSep := func(r rune) bool {
		for _, c := range sep {
			if c == r {
				return true
			}
		}
		return false
	}
	count := 0
	clean := true
	for _, r := range s {
		count++
		if inSep(r) {
			clean = false
		}
	}
	star, err := Compile("*", sep...)
	if err != nil || star == nil {
		return false
	}
	if star.Match(s) != clean {
		return false
	}
	single, err := Compile("?", sep...)
	if err != nil || single == nil {
		return false
	}
	if single.Match(s) != (count == 1 && clean) {
		return false
	}
	starSingle, err := Compile("*?", sep...)
	if err != nil || starSingle == nil {
		return false
	}
	if starSingle.Match(s) != (count >= 1 && clean) {
		return false
	}
	singleStar, err := Compile("?*", sep...)
	if err != nil || singleStar == nil {
		return false
	}
	if singleStar.Match(s) != (count >= 1 && clean) {
		return false
	}
	super, err := Compile("**", sep...)
	if err != nil || super == nil {
		return false
	}
	return super.Match(s)
}

// go2lean:sequence Compile
// SeqStarAltStar: without separators, `*{q(a),q(b)}*q(c)??` matches s exactly
// when s = x + (a or b) + y + c + two more characters, the splits falling on
// character boundaries. Commas are removed from a and b. A star after a group
// must not drop the restart points of a star before it: e.g. a = "abc",
// b = "b", c = "c" must match s = "abcXc".
func SeqStarAltStar(a string, b string, c string, s string) bool {
	clean := func(t string) string {
		out := ""
		for _, r := range t {
			if r != ',' {
				out += string(r)
			}
		}
		return out
	}
	a = clean(a)
	b = clean(b)
	c = string([]rune(c))
	p, err := Compile("*{" + QuoteMeta(a) + "," + QuoteMeta(b) + "}*" + QuoteMeta(c) + "??")
	if err != nil || p == nil {
		return false
	}
	hasAt := func(k int, lit string) bool {
		return len(s)-k >= len(lit) && s[k:k+len(lit)] == lit
	}
	bounds := func(from int) []int {
		var out []int
		for k := range s[from:] {
			out = append(out, from+k)
		}
		return append(out, len(s))
	}
	want := false
	for _, i := range bounds(0) {
		for _, alt := range []string{a, b} {
			if !hasAt(i, alt) {
				continue
			}
			for _, j := range bounds(i + len(alt)) {
				if hasAt(j, c) && len([]rune(s[j+len(c):])) == 2 {
					want = true
				}
			}
		}
	}
	return p.Match(s) == want
}

// go2lean:sequence Compile
// SeqMatchAgreesWithReference: a pattern assembled from a sequence of terms
// (quoted literal, `?`, `*`, `**`, literal alternatives `{a,b}` and `{a,}`, a
// range class, a set class, possibly negated, and a group `{a*b,?}`) compiles;
// it matches a witness string built term by term along with it; and on an
// arbitrary string s it answers what a direct backtracking reading of the
// documented syntax answers.
//
// kinds[i] % 9 selects the term; the rest of kinds[i] selects the alternative
// taken by the witness, the split of texts[i] into alternatives, negation.
// An empty literal emits nothing and is skipped, so that it can not fuse two
// `*` into a `**`.
func SeqMatchAgreesWithReference(kinds []int, texts []string, sep []rune, s string) bool {
	// Separators: valid runes only (the invalid ones are SeqStarSeparators'
	// business); 'x' is kept free for the witness of `?`.
	var seps []rune
	for _, r := range sep {
		if r < 0 || r > 0x10FFFF || (r >= 0xD800 && r <= 0xDFFF) || r == 'x' {
			continue
		}
		seps = append(seps, r)
	}
	isSep := func(r rune) bool {
		for _, c := range seps {
			if c == r {
				return true
			}
		}
		return false
	}
	isPlain := func(r rune) bool {
		return r >= 0x80 || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
	}
	nonSep := func(t string) string {
		out := ""
		for _, r := range t {
			if !isSep(r) {
				out += string(r)
			}
		}
		return out
	}
	firstChar := func(t string) string {
		for _, r := range t {
			if !isSep(r) {
				return string(r)
			}
		}
		return "x"
	}

	n := len(kinds)
	if len(texts) < n {
		n = len(texts)
	}
	var (
		tk   []int
		ta   []string
		tb   []string
		tlo  []rune
		thi  []rune
		tneg []bool
	)
	pat := ""
	w := ""
	for i := 0; i < n; i++ {
		kind := kinds[i] % 9
		if kind < 0 {
			kind = -kind
		}
		aux := kinds[i] / 9
		if aux < 0 {
			aux = -aux
		}
		choice := aux % 2
		rest := aux / 2
		rs := []rune(texts[i])
		t := string(rs)
		var cs []rune
		for _, r := range rs {
			if r != ',' {
				cs = append(cs, r)
			}
		}
		h := rest % (len(cs) + 1)
		a, b := "", ""
		var lo, hi rune
		neg := false
		switch kind {
		case 0:
			if t == "" {
				// Nothing is emitted: `*` + `` + `*` would read as `**`.
				continue
			}
			a = t
			pat += QuoteMeta(t)
			w += t
		case 1:
			pat += "?"
			w += firstChar(t)
		case 2, 3:
			if kind == 2 {
				w += nonSep(t)
			} else {
				w += t
			}
			if last := len(tk) - 1; last >= 0 && (tk[last] == 2 || tk[last] == 3) {
				// Adjacent stars: `*` then `*` is one `*`, and a `**`
				// absorbs a neighbouring `*`.
				if kind == 3 && tk[last] == 2 {
					tk[last] = 3
					pat += "*"
				}
				continue
			}
			if kind == 2 {
				pat += "*"
			} else {
				pat += "**"
			}
		case 4:
			a, b = string(cs[:h]), string(cs[h:])
			pat += "{" + QuoteMeta(a) + "," + QuoteMeta(b) + "}"
			if choice == 0 {
				w += a
			} else {
				w += b
			}
		case 5:
			lo, hi = 'a', 'z'
			if len(rs) > 0 && isPlain(rs[0]) {
				lo = rs[0]
			}
			if len(rs) > 1 && isPlain(rs[1]) {
				hi = rs[1]
			}
			if lo > hi {
				lo, hi = hi, lo
			}
			neg = choice == 1
			pat += "["
			if neg {
				pat += "!"
			}
			pat += string(lo) + "-" + string(hi) + "]"
			if neg {
				w += string(rune(1)) // lo >= '0', so 1 is outside
			} else if rest%2 == 0 {
				w += string(lo)
			} else {
				w += string(hi)
			}
		case 6:
			if len(rs) == 0 {
				rs = []rune{'a'}
			}
			neg = choice == 1
			pat += "["
			if neg {
				pat += "!"
			}
			for _, r := range rs {
				if !isPlain(r) {
					pat += "\\"
				}
				pat += string(r)
			}
			pat += "]"
			a = string(rs)
			if neg {
				c := rune(1)
				for {
					in := false
					for _, r := range rs {
						if r == c {
							in = true
						}
					}
					if !in {
						break
					}
					c++
				}
				w += string(c)
			} else {
				w += string(rs[rest%len(rs)])
			}
		case 7:
			a = string(cs)
			pat += "{" + QuoteMeta(a) + ",}"
			if choice == 0 {
				w += a
			}
		case 8:
			a, b = string(cs[:h]), string(cs[h:])
			pat += "{" + QuoteMeta(a) + "*" + QuoteMeta(b) + ",?}"
			if choice == 0 {
				w += a + nonSep(t) + b
			} else {
				w += firstChar(t)
			}
		}
		tk = append(tk, kind)
		ta = append(ta, a)
		tb = append(tb, b)
		tlo = append(tlo, lo)
		thi = append(thi, hi)
		tneg = append(tneg, neg)
	}

	g, err := Compile(pat, seps...)
	if err != nil || g == nil {
		return false
	}

	ref := func(str string) bool {
		runeAt := func(p int) (rune, int) {
			r := rune(-1)
			width := len(str) - p
			first := true
			for j, c := range str[p:] {
				if first {
					r = c
					first = false
					continue
				}
				width = j
				break
			}
			return r, width
		}
		hasAt := func(p int, lit string) bool {
			return len(str)-p >= len(lit) && str[p:p+len(lit)] == lit
		}
		var match func(i, p int) bool
		match = func(i, p int) bool {
			if i == len(tk) {
				return p == len(str)
			}
			switch tk[i] {
			case 0:
				return hasAt(p, ta[i]) && match(i+1, p+len(ta[i]))
			case 1:
				if p >= len(str) {
					return false
				}
				r, k := runeAt(p)
				return !isSep(r) && match(i+1, p+k)
			case 2, 3:
				q := p
				for {
					if match(i+1, q) {
						return true
					}
					if q >= len(str) {
						return false
					}
					r, k := runeAt(q)
					if tk[i] == 2 && isSep(r) {
						return false
					}
					q += k
				}
			case 4, 7:
				return (hasAt(p, ta[i]) && match(i+1, p+len(ta[i]))) ||
					(hasAt(p, tb[i]) && match(i+1, p+len(tb[i])))
			case 5, 6:
				if p >= len(str) {
					return false
				}
				r, k := runeAt(p)
				in := false
				if tk[i] == 5 {
					in = tlo[i] <= r && r <= thi[i]
				} else {
					for _, c := range ta[i] {
						if c == r {
							in = true
						}
					}
				}
				return in != tneg[i] && match(i+1, p+k)
			case 8:
				if hasAt(p, ta[i]) {
					q := p + len(ta[i])
					for {
						if hasAt(q, tb[i]) && match(i+1, q+len(tb[i])) {
							return true
						}
						if q >= len(str) {
							break
						}
						r, k := runeAt(q)
						if isSep(r) {
							break
						}
						q += k
					}
				}
				if p < len(str) {
					r, k := runeAt(p)
					if !isSep(r) && match(i+1, p+k) {
						return true
					}
				}
				return false
			}
			return false
		}
		return match(0, 0)
	}

	// The witness matches by construction.
	if ref(w) && !g.Match(w) {
		return false
	}
	return g.Match(s) == ref(s)
}
```
