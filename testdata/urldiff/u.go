// Package urldiff checks go2lean's model of net/url (translated from its
// Go source) against Go: every spec here fails on every input, so each
// search trial is a counterexample whose model value (a probe) M3 compares
// with the real one. Run with PKG=testdata/urldiff scripts/json-diff.sh.
package urldiff

import (
	"net/url"
	"strings"
)

func corpus(i int) string {
	if i < 0 {
		i = -i
	}
	switch i % 37 {
	case 0:
		return "https://api.deepseek.com"
	case 1:
		return "https://api.deepseek.com/v1"
	case 2:
		return "api.deepseek.com"
	case 3:
		return "https://API.Example.COM:8443/V1/Chat?x=1#frag"
	case 4:
		return "http://localhost:11434/v1"
	case 5:
		return "https://host:/p"
	case 6:
		return "https://host:abc/p"
	case 7:
		return "https://host:80:90/p"
	case 8:
		return "https://user:pw@host/p"
	case 9:
		return "https://us%20er@host/p"
	case 10:
		return "https://host/a%20b/c%2Fd"
	case 11:
		return "https://host/a%zzb"
	case 12:
		return "https://host/%"
	case 13:
		return "mailto:joe@example.com"
	case 14:
		return "/relative/path"
	case 15:
		return "relative:path"
	case 16:
		return "//host/path"
	case 17:
		return "///triple"
	case 18:
		return "https://host?"
	case 19:
		return "https://host/?q=1&r=2"
	case 20:
		return "https://host/#"
	case 21:
		return "HTTPS://Host"
	case 22:
		return "https://host/p?x=%zz"
	case 23:
		return "https://ho st/p"
	case 24:
		return "https://host/\x01"
	case 25:
		return ""
	case 26:
		return "  https://trim.example "
	case 27:
		return "https://[::1]:8080/p"
	case 28:
		return "https://例子.测试/p"
	case 29:
		return "https://host/path;params"
	case 30:
		return "file:///etc/passwd"
	case 31:
		return "https://host:65536/"
	case 32:
		return "http://127.0.0.1:5000/v1/"
	case 33:
		return "https://a.b.c.d.e/x/y/z?"
	case 34:
		return "?only=query"
	case 35:
		return "#only-frag"
	case 36:
		return "https://host/p%2"
	}
	return ""
}

// Parts renders the parts url.Parse gives a corpus entry.
func Parts(i int) string {
	u, err := url.Parse(corpus(i))
	if err != nil {
		return "err"
	}
	return strings.Join([]string{u.Scheme, u.Opaque, u.Host, u.Hostname(), u.Port(), u.Path, u.RawPath, u.RawQuery, u.Fragment}, "|")
}

// Raw parses an arbitrary string.
func Raw(s string) string {
	u, err := url.Parse(s)
	if err != nil {
		return "err"
	}
	return strings.Join([]string{u.Scheme, u.Opaque, u.Host, u.Path, u.RawQuery, u.Fragment}, "|")
}

// Provider is tingly-box's SplitProviderHostPath.
func Provider(i int) string {
	raw := strings.TrimSpace(corpus(i))
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "|"
	}
	return strings.ToLower(u.Hostname()) + "|" + strings.ToLower(u.Path)
}
