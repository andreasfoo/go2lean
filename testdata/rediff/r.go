// Package rediff checks go2lean's regexp model against Go: every spec here
// fails on every input, so each search trial is a counterexample whose
// model value (a probe) M3 compares with the real one. Run with
// PKG=testdata/rediff scripts/json-diff.sh.
package rediff

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	reVer   = regexp.MustCompile(`^v(\d+)\.(\d+)(?:\.(\d+))?$`)
	reMail  = regexp.MustCompile(`\b(\w+)@(\w+)\.com\b`)
	reFold  = regexp.MustCompile(`(?i)hello|wor(ld)?`)
	reAlt   = regexp.MustCompile(`(a|ab)(c|bcd)(d*)`)
	reUni   = regexp.MustCompile(`[α-ω]+|é`)
	reLines = regexp.MustCompile(`(?m)^x+$`)
	reStar  = regexp.MustCompile(`a*`)
	reNot   = regexp.MustCompile(`[^a-c]{2,3}`)
	reDot   = regexp.MustCompile(`(.)(.*?)(\.)`)
)

// show renders what four methods of a regexp gave on a string.
func show(match bool, found string, ix []int, m []string) string {
	var b strings.Builder
	if match {
		b.WriteString("T|")
	} else {
		b.WriteString("F|")
	}
	b.WriteString(found)
	b.WriteString("|")
	if ix != nil {
		b.WriteString(strconv.Itoa(ix[0]) + "," + strconv.Itoa(ix[1]))
	} else {
		b.WriteString("nil")
	}
	b.WriteString("|")
	if m != nil {
		b.WriteString(strconv.Itoa(len(m)) + ":" + strings.Join(m, ","))
	} else {
		b.WriteString("nil")
	}
	return b.String()
}

func Ver(s string) string {
	return show(reVer.MatchString(s), reVer.FindString(s), reVer.FindStringIndex(s), reVer.FindStringSubmatch(s))
}

func Mail(s string) string {
	return show(reMail.MatchString(s), reMail.FindString(s), reMail.FindStringIndex(s), reMail.FindStringSubmatch(s))
}

func Fold(s string) string {
	return show(reFold.MatchString(s), reFold.FindString(s), reFold.FindStringIndex(s), reFold.FindStringSubmatch(s))
}

func Alt(s string) string {
	return show(reAlt.MatchString(s), reAlt.FindString(s), reAlt.FindStringIndex(s), reAlt.FindStringSubmatch(s))
}

func Uni(s string) string {
	return show(reUni.MatchString(s), reUni.FindString(s), reUni.FindStringIndex(s), reUni.FindStringSubmatch(s))
}

func Lines(s string) string {
	return show(reLines.MatchString(s), reLines.FindString(s), reLines.FindStringIndex(s), reLines.FindStringSubmatch(s))
}

func Star(s string) string {
	return show(reStar.MatchString(s), reStar.FindString(s), reStar.FindStringIndex(s), reStar.FindStringSubmatch(s))
}

func Not(s string) string {
	return show(reNot.MatchString(s), reNot.FindString(s), reNot.FindStringIndex(s), reNot.FindStringSubmatch(s))
}

func Dot(s string) string {
	return show(reDot.MatchString(s), reDot.FindString(s), reDot.FindStringIndex(s), reDot.FindStringSubmatch(s))
}

// Pkg is regexp.MatchString with a constant pattern.
func Pkg(s string) string {
	ok, err := regexp.MatchString(`^[a-z]+\d?$`, s)
	r := "F"
	if ok {
		r = "T"
	}
	if err != nil {
		r += "!"
	}
	return r
}

// corpus picks an edge case of the patterns by index (the search covers
// small integers well; random strings rarely match).
func corpus(i int) string {
	if i < 0 {
		i = -i
	}
	switch i % 30 {
	case 0:
		return "v1.2"
	case 1:
		return "v10.20.30"
	case 2:
		return "v1.2."
	case 3:
		return "xv1.2"
	case 4:
		return "joe@example.com"
	case 5:
		return "a joe@x.com b"
	case 6:
		return "joe@x.co"
	case 7:
		return "HeLLo"
	case 8:
		return "WORLD"
	case 9:
		return "wor"
	case 10:
		return "Hellow"
	case 11:
		return "abcd"
	case 12:
		return "abcbcd"
	case 13:
		return "abd"
	case 14:
		return "abcdd"
	case 15:
		return "αβγ"
	case 16:
		return "aéb"
	case 17:
		return "é"
	case 18:
		return "x\nxx"
	case 19:
		return "\nx"
	case 20:
		return "x\n"
	case 21:
		return "aaa"
	case 22:
		return ""
	case 23:
		return "xyz"
	case 24:
		return "abcdefg"
	case 25:
		return "def"
	case 26:
		return "a.b.c."
	case 27:
		return ".."
	case 28:
		return "ab."
	case 29:
		return "9"
	}
	return ""
}

func VerAt(i int) string   { return Ver(corpus(i)) }
func MailAt(i int) string  { return Mail(corpus(i)) }
func FoldAt(i int) string  { return Fold(corpus(i)) }
func AltAt(i int) string   { return Alt(corpus(i)) }
func UniAt(i int) string   { return Uni(corpus(i)) }
func LinesAt(i int) string { return Lines(corpus(i)) }
func StarAt(i int) string  { return Star(corpus(i)) }
func NotAt(i int) string   { return Not(corpus(i)) }
func DotAt(i int) string   { return Dot(corpus(i)) }
