# go2lean report: `github.com/gobwas/glob/syntax`

## 1. Confirmed bugs

The model found one confirmed counterexample. Go's fuzzer, running the real code, found three violated specs, and all three fail on the input `"[]"`. Tracing the real code shows two separate defects behind them. I found a third defect by reading the source. No property covered it, so the revised spec file adds a check for it.

### 1a. Character classes: `Offset` runs ahead of the returned tokens (fuzzer, real code)

- **Properties:** `SeqOffsetAfterEachToken` and `SeqOffsetMonotonicBounded`. The fuzzer reported both as violated on `[]byte("[]")`.
- **What the real run did:** when `fetchItem` sees `[` (lexer.go, `case r == char_range_open:`), it calls `fetchRange`, which lexes the whole class to `]` before `Next` returns `RangeOpen`. `Offset` simply returns `l.pos` (`func (l *Lexer) Offset() int { return l.pos }`).
  - For `"[]"`: after `RangeOpen`, `Offset()` is 2, not 1. After `RangeClose` it is still 2, so the offset does not advance.
  - Every class does the same. For example, `"[a]"` gives `Offset() == 3` right after `RangeOpen`.
- **Why this is a bug:** the doc comment on `Offset` says it is "the position right after the most recently returned token". Inside a class the code does not do that. A parser that uses `Offset` for error positions will point past the class.
- **Fix:** the one-line fix is to change the doc on `Offset` to say "the byte offset of input consumed so far; a character class is consumed whole when its RangeOpen is returned". If the documented behaviour is what you want, record each token's end position when you push it (for example, a parallel `[]int` next to `tokens`) and have `Offset` return the end of the last token that was shifted.

### 1b. An empty class `[]` / `[!]` is accepted without an error (fuzzer, real code)

- **Property:** `SeqTokenGrammar`, violated on `[]byte("[]")`.
- **What the real run did:**
  1. In `fetchRange`, `r = ']'` and `peek()` gives `eof`, so the code reaches `l.unread()` and `l.fetchText([]rune{char_range_close})`.
  2. `fetchText` stops at once on the breaker `]` with `len(data) == 0`, so it pushes no Text (`if len(data) > 0 {`).
  3. `wantClose = true`, and the next read is `]`.
  4. The stream comes out as `RangeOpen "[" , RangeClose "]" , EOF`. `"[!]"` gives `RangeOpen, Not, RangeClose`.
- **Why this is a bug:** the `RangeOpen`/`RangeClose` doc says the lexer puts either one Text or a RangeLo/RangeBetween/RangeHi triple between them. The `Error` doc says malformed classes are reported by the lexer. An empty class is malformed under either reading: as an empty set, or under POSIX rules where `[]` would be an unclosed class.
- **Fix:** in `fetchRange`, record `n := len(l.tokens)` before the `l.fetchText([]rune{char_range_close})` call, and add this after it:
  ```go
  if len(l.tokens) == n { l.errorf("empty character class"); return }
  ```

### 1c. A later error overwrites the first one (found by reading the source; the revised specs check it)

`errorf` assigns unconditionally (`l.err = fmt.Errorf(f, v...)`). `peek` reports invalid UTF-8 by setting the error and returning `eof`, and the caller then reports its own "end of input" error on top of it:

- `"a\\\xff"`: `fetchText` sees `eof` while `escaped` is set, so the message becomes `"trailing backslash"`.
- `"[\xff]"`, or `"[ab\xff]"`: `fetchRange` sees `eof`, so the message becomes `"unexpected end of input"`.

In both cases the real cause, `"invalid UTF-8 sequence"`, is lost.

**Fix:** in `errorf`, keep the first error:

```go
if l.err == nil { l.err = fmt.Errorf(f, v...) }
```

The new spec `SeqInvalidUTF8IsReported` checks this.

### 1d. `tokens.shift` panics on an empty queue (low severity)

- **Property:** `tokens_shift_nopanic`, confirmed.
- **What the real run did:** `ret = (*i)[0]` raised `index out of range [0] with length 0`.
- **Reachability:** the only caller, `Next`, calls it under `if !l.tokens.empty()`, so the public API cannot reach this panic.
- **Fix:** the property is too strong for an unexported helper with a precondition. Restate it as "`shift` does not panic when `len(*i) > 0`". If you want the helper to be total, add:
  ```go
  if len(*i) == 0 { return Token{EOF, ""} }
  ```

## 2. Spurious counterexamples

None of these were problems with the properties. In every case the model behaved differently from Go, and the real run was fine.

- **Model panics with `slice_bounds` where the real code does not.** This covers the no-panic properties for:
  - `Lexer.Next`, `fetchItem`, `fetchRange` and `fetchText`
  - all ten sequence specs

  For example, the bytes in `"could not unread rune"` gave real `ret:true`. The real run never panicked and the oracle held every time. This points to a modelling gap around the `l.data[l.pos:]` slicing after `seek(-lastRuneSize)`/`unread`, or around the havoc'd `breakers` slice. The specs themselves are fine. The `Next` case is odd: the real run returned `Text " could not unread rune"` and did not panic.
- **`IsSpecial` specs (`c=219`).** The model picked a value for the unmodelled `bytealg.IndexByte` call. The real run returned `ret=false`, and both oracles hold for that.
- **`Token.String` spec.** The model picked an empty result for the unmodelled `fmt.Sprintf` call. The real run returned `<unknown token type><"range_clos">`, which satisfies the oracle.

The three fuzzer violations in section 1 are real behaviour, not spurious results. Each spec still bundled one defect with properties that do hold, so the first failure masked everything else the spec checks. The revision splits them:

- `SeqOffsetAfterEachToken`: outside a class, `Offset == sum of Data`; inside a class, `Offset >= sum`. The new spec `SeqOffsetRightAfterRangeOpen` carries the doc contract on its own.
- `SeqOffsetMonotonicBounded`: the offset must strictly advance except for tokens inside a class. "Non-decreasing" and "bounded by `len(src)`" are unchanged.
- `SeqTokenGrammar`: allows `RangeOpen (Not) RangeClose`. The empty class is now checked on its own by `SeqEmptyClassIsError`.

`SeqOffsetRightAfterRangeOpen`, `SeqEmptyClassIsError` and `SeqInvalidUTF8IsReported` are meant to fail until 1a, 1b and 1c are fixed. For 1a, a doc-only fix means `SeqOffsetRightAfterRangeOpen` should be deleted instead.

## 3. Properties that held

**Certified by the exhaustive engine.** The domain is boundary values, |v|≤8, slices up to length 3, and the strings `""`/`"a"`.

- `exhausted`: no-panic for
  - `NewLexer`, `TokenType.String`
  - `Lexer.Offset`, `inTerms`, `seek`, `termsEnter`, `termsLeave`, `unread`
- `exhausted_havoc` (unmodelled calls tried with three values): `Token.String` no-panic.
- `exhausted_assuming` (one under-approximation): `IsSpecial`, `Lexer.peek` and `Lexer.read` no-panic.
- `SpecTokenTypeStringNamesEveryKind`: exhausted over 28 inputs, all of which got past the guards, and all 24 mutants fail it. This spec has real teeth.

The lexer no-panic results only cover the sources `""` and `"a"`, which is a very small domain. The stronger evidence is that the fuzzer ran the sequence specs on real code for about 30 seconds with no panic.

**Only "no counterexample found":**

- `tokens.empty`, `tokens.push` and `errorf` no-panic: random search only.
- All other specs held under the fuzzer for about 30 seconds:
  - both `IsSpecial` specs
  - `SeqEndIsSticky`, `SeqEscapedSpecialsLexAsOneText`, `SeqEscapedEveryRuneLexAsOneText`
  - `SeqNextConservesSource`, `SeqNoErrorWithoutClassOrBadUTF8`
  - `SeqRangeTriple`, `SeqTermsAlternativesLexAsTexts`
  - `SpecTokenStringShowsType`

  The model diverged on all of these, so none has mutation data.

**Weak specs:**

- `SpecTokenStringShowsType` only checks `name<` … `>`. A wrong format, such as dropping `%q` or the Data, would pass it. The revision requires the exact `name<"Data">` whenever Data is printable ASCII with no `"` or `\`.
- The specs that `return true` on `Error` (`SeqNextConservesSource`, `SeqOffset*`, `SeqTokenGrammar`) pass vacuously for a mutant that makes the lexer error on everything. That kind of mutant is still caught by the specs that have no Error escape: `SeqNoErrorWithoutClassOrBadUTF8`, `SeqEscaped*`, `SeqRangeTriple` and `SeqTermsAlternativesLexAsTexts`. I kept those specs unchanged for that reason.

**Side note, not checked by any property:** inside a class a backslash is not an escape when it is RangeLo or RangeHi. `"[\\-a]"` lexes as the range `\`–`a`, while `"[\\ab]"` lexes as Text `ab`. Please confirm that this is intended.

```go
package syntax

// go2lean:sequence Lexer.Next
// SeqNextConservesSource: for a pattern without backslashes that lexes
// without error, the Data of the tokens returned before EOF are all
// non-empty and concatenate back to the source, and EOF arrives within
// len(source)+1 calls.
func SeqNextConservesSource(events []byte) bool {
	var b []byte
	for _, c := range events {
		if c != '\\' {
			b = append(b, c)
		}
	}
	src := string(b)
	l := NewLexer(src)
	got := ""
	for i := 0; i <= len(src)+1; i++ {
		t := l.Next()
		if t.Type == Error {
			return true
		}
		if t.Type == EOF {
			return got == src
		}
		if t.Data == "" {
			return false
		}
		got += t.Data
	}
	return false
}

// go2lean:sequence Lexer.Next
// SeqOffsetAfterEachToken: without backslashes every token's Data is its
// source text. Outside a character class, Offset after each token equals
// the summed Data lengths; inside a class (RangeOpen up to, excluding,
// RangeClose) the lexer has already consumed the whole class, so Offset is
// at least that sum. At EOF Offset equals len(source).
func SeqOffsetAfterEachToken(events []byte) bool {
	var b []byte
	for _, c := range events {
		if c != '\\' {
			b = append(b, c)
		}
	}
	src := string(b)
	l := NewLexer(src)
	if l.Offset() != 0 {
		return false
	}
	sum := 0
	inClass := false
	for i := 0; i <= len(src)+1; i++ {
		t := l.Next()
		if t.Type == Error {
			return true
		}
		if t.Type == EOF {
			return l.Offset() == len(src)
		}
		sum += len(t.Data)
		if t.Type == RangeOpen {
			inClass = true
		}
		if t.Type == RangeClose {
			inClass = false
		}
		if inClass {
			if l.Offset() < sum {
				return false
			}
		} else if l.Offset() != sum {
			return false
		}
	}
	return false
}

// go2lean:sequence Lexer.Next
// SeqOffsetRightAfterRangeOpen: Offset is documented as the position right
// after the most recently returned token, so after the RangeOpen of
// "[" + x + "]" it is 1. (Fails today: the class is lexed whole.)
func SeqOffsetRightAfterRangeOpen(x byte) bool {
	x = 'a' + x%26
	l := NewLexer(string([]byte{'[', x, ']'}))
	t := l.Next()
	if t.Type != RangeOpen {
		return false
	}
	return l.Offset() == 1
}

// go2lean:sequence Lexer.Next
// SeqOffsetMonotonicBounded: for any source (escapes and invalid UTF-8
// included) Offset starts at 0, never decreases, never passes len(source),
// and strictly advances with every non-EOF, non-Error token that is not
// inside a character class (the tokens after RangeOpen share its offset).
func SeqOffsetMonotonicBounded(events []byte) bool {
	src := string(events)
	l := NewLexer(src)
	prev := l.Offset()
	if prev != 0 {
		return false
	}
	inClass := false
	for i := 0; i <= len(src)+1; i++ {
		t := l.Next()
		off := l.Offset()
		if off < prev || off > len(src) {
			return false
		}
		if t.Type == EOF || t.Type == Error {
			return true
		}
		if off == prev && !inClass {
			return false
		}
		if t.Type == RangeOpen {
			inClass = true
		}
		if t.Type == RangeClose {
			inClass = false
		}
		prev = off
	}
	return false
}

// go2lean:sequence Lexer.Next
// SeqTokenGrammar: up to EOF (or an Error) the token stream obeys the
// grammar: a character class is RangeOpen, optional Not, then at most one
// Text or one RangeLo RangeBetween RangeHi triple, then RangeClose (the
// empty class is checked separately by SeqEmptyClassIsError);
// Not/RangeLo/RangeBetween/RangeHi/RangeClose never appear outside a
// class; TermSeparator and TermsClose appear only inside an open group;
// EOF never arrives inside an unfinished class; each token carries the
// documented Data.
func SeqTokenGrammar(events []byte) bool {
	src := string(events)
	l := NewLexer(src)
	depth := 0
	// cls: 0 outside a class, 1 after RangeOpen, 2 after Not, 3 after
	// RangeLo, 4 after RangeBetween, 5 waiting for RangeClose.
	cls := 0
	for i := 0; i <= len(src)+1; i++ {
		t := l.Next()
		if t.Type == Error {
			return true
		}
		if t.Type == EOF {
			return cls == 0 && t.Data == ""
		}
		switch t.Type {
		case Text:
			if t.Data == "" {
				return false
			}
		case Any:
			if t.Data != "*" {
				return false
			}
		case Super:
			if t.Data != "**" {
				return false
			}
		case Single:
			if t.Data != "?" {
				return false
			}
		case Not:
			if t.Data != "!" {
				return false
			}
		case TermSeparator:
			if t.Data != "," {
				return false
			}
		case RangeOpen:
			if t.Data != "[" {
				return false
			}
		case RangeClose:
			if t.Data != "]" {
				return false
			}
		case RangeBetween:
			if t.Data != "-" {
				return false
			}
		case TermsOpen:
			if t.Data != "{" {
				return false
			}
		case TermsClose:
			if t.Data != "}" {
				return false
			}
		case RangeLo, RangeHi:
			if len([]rune(t.Data)) != 1 {
				return false
			}
		default:
			return false
		}
		if cls != 0 {
			switch {
			case cls == 1 && t.Type == Not:
				cls = 2
			case (cls == 1 || cls == 2) && t.Type == Text:
				cls = 5
			case (cls == 1 || cls == 2) && t.Type == RangeLo:
				cls = 3
			case cls == 3 && t.Type == RangeBetween:
				cls = 4
			case cls == 4 && t.Type == RangeHi:
				cls = 5
			case (cls == 1 || cls == 2 || cls == 5) && t.Type == RangeClose:
				cls = 0
			default:
				return false
			}
			continue
		}
		switch t.Type {
		case RangeOpen:
			cls = 1
		case TermsOpen:
			depth++
		case TermsClose:
			if depth == 0 {
				return false
			}
			depth--
		case TermSeparator:
			if depth == 0 {
				return false
			}
		case Text, Any, Super, Single:
		default:
			return false
		}
	}
	return false
}

// go2lean:sequence Lexer.Next
// SeqEmptyClassIsError: "[]" and "[!]" are malformed character classes,
// which the lexer is documented to report: an Error comes before EOF.
// (Fails today: they lex as RangeOpen (Not) RangeClose.)
func SeqEmptyClassIsError(not bool) bool {
	p := "[]"
	if not {
		p = "[!]"
	}
	l := NewLexer(p)
	for i := 0; i < 5; i++ {
		t := l.Next()
		if t.Type == Error {
			return true
		}
		if t.Type == EOF {
			return false
		}
	}
	return false
}

// go2lean:sequence Lexer.Next
// SeqInvalidUTF8IsReported: a pattern whose only problem is an invalid
// UTF-8 byte (after a trailing-looking backslash, or inside a class)
// yields the Error "invalid UTF-8 sequence", not a later follow-on error.
func SeqInvalidUTF8IsReported(events []byte, inClass bool) bool {
	s := string([]rune(string(events)))
	var p []byte
	if inClass {
		p = append(p, '[')
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '[' || c == ']' || c == '\\' || c == '-' || c == '!' {
			continue
		}
		p = append(p, c)
	}
	if inClass {
		p = append(p, 0xff, ']')
	} else {
		p = append(p, '\\', 0xff)
	}
	l := NewLexer(string(p))
	for i := 0; i <= len(p)+1; i++ {
		t := l.Next()
		if t.Type == Error {
			return t.Data == "invalid UTF-8 sequence"
		}
		if t.Type == EOF {
			return false
		}
	}
	return false
}

// go2lean:sequence Lexer.Next
// SeqEndIsSticky: once Next returns EOF or Error it keeps returning the
// same token; at EOF the Offset is len(source).
func SeqEndIsSticky(events []byte) bool {
	src := string(events)
	l := NewLexer(src)
	for i := 0; i <= len(src)+1; i++ {
		t := l.Next()
		if t.Type == EOF || t.Type == Error {
			if t.Type == EOF && l.Offset() != len(src) {
				return false
			}
			if t.Type == Error && t.Data == "" {
				return false
			}
			for k := 0; k < 2; k++ {
				u := l.Next()
				if u.Type != t.Type || u.Data != t.Data {
					return false
				}
			}
			return true
		}
	}
	return false
}

// go2lean:sequence Lexer.Next
// SeqEscapedSpecialsLexAsOneText: escaping the meta characters (those
// IsSpecial reports, the ones glob.QuoteMeta escapes) of any valid UTF-8
// string s gives a pattern that lexes as a single Text token equal to s,
// then EOF at the end of the pattern. Outside a group or class ',', '!'
// and '-' need no escaping.
func SeqEscapedSpecialsLexAsOneText(events []byte) bool {
	s := string([]rune(string(events)))
	var p []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '*' || c == '?' || c == '\\' || c == '[' || c == ']' || c == '{' || c == '}' {
			p = append(p, '\\')
		}
		p = append(p, c)
	}
	l := NewLexer(string(p))
	t := l.Next()
	if s != "" {
		if t.Type != Text || t.Data != s {
			return false
		}
		t = l.Next()
	}
	return t.Type == EOF && l.Offset() == len(p)
}

// go2lean:sequence Lexer.Next
// SeqEscapedEveryRuneLexAsOneText: a backslash before every rune of a
// valid UTF-8 string s gives a pattern that lexes as one Text token equal
// to s (escapes resolved), then EOF.
func SeqEscapedEveryRuneLexAsOneText(events []byte) bool {
	s := string([]rune(string(events)))
	p := ""
	for _, r := range s {
		p += "\\" + string(r)
	}
	l := NewLexer(p)
	t := l.Next()
	if s != "" {
		if t.Type != Text || t.Data != s {
			return false
		}
		t = l.Next()
	}
	return t.Type == EOF && l.Offset() == len(p)
}

// go2lean:sequence Lexer.Next
// SeqTermsAlternativesLexAsTexts: "{" + esc(a) + "," + esc(b) + "}", with
// the meta characters and ',' escaped, lexes as TermsOpen, Text(a) (absent
// when a is empty), TermSeparator, Text(b) (absent when b is empty),
// TermsClose, EOF.
func SeqTermsAlternativesLexAsTexts(ea []byte, eb []byte) bool {
	a := string([]rune(string(ea)))
	b := string([]rune(string(eb)))
	p := []byte{'{'}
	for i := 0; i < len(a); i++ {
		c := a[i]
		if c == '*' || c == '?' || c == '\\' || c == '[' || c == ']' || c == '{' || c == '}' || c == ',' {
			p = append(p, '\\')
		}
		p = append(p, c)
	}
	p = append(p, ',')
	for i := 0; i < len(b); i++ {
		c := b[i]
		if c == '*' || c == '?' || c == '\\' || c == '[' || c == ']' || c == '{' || c == '}' || c == ',' {
			p = append(p, '\\')
		}
		p = append(p, c)
	}
	p = append(p, '}')
	l := NewLexer(string(p))
	t := l.Next()
	if t.Type != TermsOpen {
		return false
	}
	t = l.Next()
	if a != "" {
		if t.Type != Text || t.Data != a {
			return false
		}
		t = l.Next()
	}
	if t.Type != TermSeparator {
		return false
	}
	t = l.Next()
	if b != "" {
		if t.Type != Text || t.Data != b {
			return false
		}
		t = l.Next()
	}
	if t.Type != TermsClose {
		return false
	}
	t = l.Next()
	return t.Type == EOF && l.Offset() == len(p)
}

// go2lean:sequence Lexer.Next
// SeqRangeTriple: "[" + optional "!" + lo + "-" + hi + "]" for ASCII lo
// and hi lexes as RangeOpen, (Not), RangeLo(lo), RangeBetween,
// RangeHi(hi), RangeClose, EOF.
func SeqRangeTriple(lo byte, hi byte, not bool) bool {
	lo = lo & 0x7f
	hi = hi & 0x7f
	if lo == '\\' {
		lo = 'a'
	}
	if hi == '\\' {
		hi = 'z'
	}
	if lo == '!' {
		not = true
	}
	p := []byte{'['}
	if not {
		p = append(p, '!')
	}
	p = append(p, lo, '-', hi, ']')
	l := NewLexer(string(p))
	t := l.Next()
	if t.Type != RangeOpen {
		return false
	}
	if not {
		t = l.Next()
		if t.Type != Not {
			return false
		}
	}
	t = l.Next()
	if t.Type != RangeLo || t.Data != string([]byte{lo}) {
		return false
	}
	t = l.Next()
	if t.Type != RangeBetween {
		return false
	}
	t = l.Next()
	if t.Type != RangeHi || t.Data != string([]byte{hi}) {
		return false
	}
	t = l.Next()
	if t.Type != RangeClose {
		return false
	}
	t = l.Next()
	return t.Type == EOF && l.Offset() == len(p)
}

// go2lean:sequence Lexer.Next
// SeqNoErrorWithoutClassOrBadUTF8: the lexer reports only token-local
// errors (invalid UTF-8, malformed class, trailing backslash). A valid
// UTF-8 pattern with no '[' and no trailing unescaped backslash lexes to
// EOF without Error -- structural problems such as an unclosed '{' are
// the parser's, not the lexer's.
func SeqNoErrorWithoutClassOrBadUTF8(events []byte) bool {
	s := string([]rune(string(events)))
	var p []byte
	run := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '[' {
			continue
		}
		p = append(p, s[i])
		if s[i] == '\\' {
			run++
		} else {
			run = 0
		}
	}
	if run%2 == 1 {
		p = append(p, 'x')
	}
	l := NewLexer(string(p))
	for i := 0; i <= len(p)+1; i++ {
		t := l.Next()
		if t.Type == Error {
			return false
		}
		if t.Type == EOF {
			return true
		}
	}
	return false
}

// go2lean:spec IsSpecial
// SpecIsSpecialExactlyMeta: IsSpecial is true exactly for the meta
// characters * ? \ [ ] { } and false for everything else, including
// ',', '!' and '-'.
func SpecIsSpecialExactlyMeta(c byte, ret bool) bool {
	want := c == '*' || c == '?' || c == '\\' || c == '[' || c == ']' || c == '{' || c == '}'
	return ret == want
}

// go2lean:spec IsSpecial
// SpecIsSpecialAgreesWithLexer: an ASCII byte IsSpecial rejects lexes,
// alone, as a Text token of itself; one it accepts lexes as Text of
// itself once escaped. Non-ASCII bytes are never special.
func SpecIsSpecialAgreesWithLexer(c byte, ret bool) bool {
	if c >= 0x80 {
		return !ret
	}
	var p []byte
	if ret {
		p = []byte{'\\', c}
	} else {
		p = []byte{c}
	}
	l := NewLexer(string(p))
	t := l.Next()
	if t.Type != Text || t.Data != string([]byte{c}) {
		return false
	}
	return l.Next().Type == EOF
}

// go2lean:spec TokenType.String
// SpecTokenTypeStringNamesEveryKind: every defined TokenType has its own
// non-empty name, distinct from every other kind's and from the unknown
// marker; values outside the enum give the unknown marker.
func SpecTokenTypeStringNamesEveryKind(tt TokenType, ret string) bool {
	if tt < EOF || tt > TermsClose {
		return ret == "<unknown token type>"
	}
	if ret == "" || ret == "<unknown token type>" {
		return false
	}
	for u := EOF; u <= TermsClose; u++ {
		if u != tt && u.String() == ret {
			return false
		}
	}
	return true
}

// go2lean:spec Token.String
// SpecTokenStringShowsType: Token.String is the type's name, then the
// quoted Data in angle brackets. When Data is printable ASCII without '"'
// or '\', the quoting adds only the two double quotes, so the result is
// exactly name + "<\"" + Data + "\">"; otherwise it still starts with
// name + "<\"" and ends with "\">".
func SpecTokenStringShowsType(t Token, ret string) bool {
	name := t.Type.String()
	plain := true
	for i := 0; i < len(t.Data); i++ {
		c := t.Data[i]
		if c < 0x20 || c > 0x7e || c == '"' || c == '\\' {
			plain = false
		}
	}
	if plain {
		return ret == name+"<\""+t.Data+"\">"
	}
	if len(ret) < len(name)+4 {
		return false
	}
	if ret[:len(name)+2] != name+"<\"" {
		return false
	}
	return ret[len(ret)-2:] == "\">"
}
```
