```go
package glob

// go2lean:spec Compile
// SpecCompileLiteralAccepted: a pattern made only of plain characters (none of
// `*`, `?`, `\`, `[`, `{`, `}`; the grammar lets every other character, `,`
// and `]` included, stand for itself) compiles, and String returns it.
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
	return ret0.String() == pattern
}

// go2lean:spec Compile
// SpecCompileLiteralMatchesExactly: a pattern of plain characters matches its
// own text and nothing longer or shorter.
func SpecCompileLiteralMatchesExactly(pattern string, separators []rune, ret0 *Pattern, ret1 error) bool {
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*', '?', '\\', '[', '{', '}':
			return true
		}
	}
	if ret1 != nil || ret0 == nil {
		return false
	}
	if !ret0.Match(pattern) {
		return false
	}
	if ret0.Match(pattern + "x") {
		return false
	}
	if len(pattern) > 0 && ret0.Match(pattern[:len(pattern)-1]) {
		return false
	}
	return true
}

// go2lean:spec Compile
// SpecCompileResultShape: exactly one of the results is set; an error is a
// *SyntaxError whose Offset is a byte offset inside the pattern; a compiled
// pattern reports its source text and the separators it was given.
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
	return true
}

// go2lean:spec Compile
// SpecCompileBraceBalance: for a pattern without escapes and classes, Compile
// fails exactly when its braces are unbalanced (a `}` without its `{`, or an
// unclosed `{`); `{}`, `{a,}`, top-level `,`, `*`, `**` and `?` are all valid.
func SpecCompileBraceBalance(pattern string, separators []rune, ret0 *Pattern, ret1 error) bool {
	depth := 0
	bad := false
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\', '[':
			return true
		case '{':
			depth++
		case '}':
			depth--
			if depth < 0 {
				bad = true
			}
		}
	}
	if depth != 0 {
		bad = true
	}
	if bad {
		return ret1 != nil && ret0 == nil
	}
	return ret1 == nil && ret0 != nil
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
// equal one of them.
func SeqAlternativesExact(alts []string, t string) bool {
	if len(alts) == 0 {
		alts = []string{""}
	}
	valid := true
	pat := "{"
	for i, a := range alts {
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
	for _, a := range alts {
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
// SeqMatchAgreesWithReference: a pattern assembled from a sequence of terms
// (quoted literal, `?`, `*`, `**`, literal alternatives `{a,b}` and `{a,}`, a
// range class, a set class, possibly negated, and a group `{a*b,?}`) compiles;
// it matches a witness string built term by term along with it; and on an
// arbitrary string s it answers what a direct backtracking reading of the
// documented syntax answers.
//
// kinds[i] % 9 selects the term; the rest of kinds[i] selects the alternative
// taken by the witness, the split of texts[i] into alternatives, negation.
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

The strongest checks are the two sequence specs. SeqMatchAgreesWithReference compares Match against a direct backtracking reading of the grammar, on a witness string and on a free string. It covers star restarts across alternatives of different lengths, the star jumps, the folded matchers and the minLen/suffix preconditions. SeqAlternativesExact and SpecCommonSuffixIsCommonSuffix test the required-suffix computation on any bytes.
