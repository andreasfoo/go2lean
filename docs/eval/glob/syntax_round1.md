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
// SeqOffsetAfterEachToken: Offset is the position right after the most
// recently returned token. Without backslashes every token's Data is its
// source text, so after each token Offset equals the summed Data lengths,
// and at EOF it equals len(source).
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
	for i := 0; i <= len(src)+1; i++ {
		t := l.Next()
		if t.Type == Error {
			return true
		}
		if t.Type == EOF {
			return l.Offset() == len(src)
		}
		sum += len(t.Data)
		if l.Offset() != sum {
			return false
		}
	}
	return false
}

// go2lean:sequence Lexer.Next
// SeqOffsetMonotonicBounded: for any source (escapes and invalid UTF-8
// included) Offset starts at 0, never decreases, never passes len(source),
// and strictly advances with every non-EOF, non-Error token (each token is
// read from at least one source byte).
func SeqOffsetMonotonicBounded(events []byte) bool {
	src := string(events)
	l := NewLexer(src)
	prev := l.Offset()
	if prev != 0 {
		return false
	}
	for i := 0; i <= len(src)+1; i++ {
		t := l.Next()
		off := l.Offset()
		if off < prev || off > len(src) {
			return false
		}
		if t.Type == EOF || t.Type == Error {
			return true
		}
		if off == prev {
			return false
		}
		prev = off
	}
	return false
}

// go2lean:sequence Lexer.Next
// SeqTokenGrammar: up to EOF (or an Error) the token stream obeys the
// documented grammar: a character class is RangeOpen, optional Not, then
// either one Text or a RangeLo RangeBetween RangeHi triple, then
// RangeClose; Not/RangeLo/RangeBetween/RangeHi/RangeClose never appear
// outside a class; TermSeparator and TermsClose appear only inside an open
// group; EOF never arrives inside an unfinished class; each token carries
// the documented Data.
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
			case cls == 5 && t.Type == RangeClose:
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
// SpecTokenStringShowsType: Token.String starts with the type's name
// followed by '<' and ends with '>'.
func SpecTokenStringShowsType(t Token, ret string) bool {
	name := t.Type.String()
	if len(ret) < len(name)+2 {
		return false
	}
	if ret[:len(name)+1] != name+"<" {
		return false
	}
	return ret[len(ret)-1] == '>'
}
```
