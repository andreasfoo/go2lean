# Oracle catalog

One oracle per target, the strongest the code supports. Each entry says
where it applies, which real defects it caught or would have caught (from
the sample in docs/eval/go-defects.md), and how to write it as a go2lean spec. The
spec format is in writing-specs.md.

**Where it runs.** Some oracles run better in `go test -fuzz` than in the
model (`go2lean fuzz` exports the same spec):
- floats are opaque in the model;
- the model ranges over a map in insertion order, so it cannot see order
  dependence;
- the model does not run with 32-bit `int`.

## 1. Documented contract

**Where:** a doc comment says what the result is or guarantees ("returns
the part of s before the first sep; s if sep is absent", "never returns
an empty slice", "the result is at most n bytes").

**Defects:** cobra removing a flag value equal to a subcommand name;
humanize printing "1 seconds"; chi's content-type list with a trailing
space; gin's `pathFor`.

**How:** state what the doc promises as a relation on the result, without
recomputing it. Quote the doc line in the spec's comment, so a failure can
be judged against it.

```go
// go2lean:spec Before
// SpecBeforeDoc: "Before returns the substring of s before the first
// occurrence of char; s itself when char does not occur."
func SpecBeforeDoc(s, char string, ret string) bool {
	if char == "" || !strings.Contains(s, char) {
		return ret == s
	}
	return strings.HasPrefix(s, ret+char) && !strings.Contains(ret, char)
}
```

## 2. Agreement with a reference

**Where:** the code implements something that already exists:
- a parser or formatter for a standard (dates, URLs, JSON, Markdown,
  shell words);
- a reimplementation of a standard-library function;
- a fast path next to a slow path;
- a port of another implementation.

**Defects:** snappy diverging from the C++ implementation; goldmark and
blackfriday against CommonMark; uniseg against Unicode's break rules; the
CIE94 formula.

**How:** compare with the reference on the same input. When the reference
is a test-vector file (CommonMark's spec examples, Unicode's
`GraphemeBreakTest.txt`), use a table-driven Go test instead of a spec.
The vectors are the oracle, and go2lean adds nothing to a fixed table.

```go
// go2lean:spec Atoi
// SpecAtoiAgreesWithStrconv: the package's Atoi matches strconv.Atoi
// (value and whether it fails).
func SpecAtoiAgreesWithStrconv(s string, ret int, ret1 error) bool {
	want, err := strconv.Atoi(s)
	if (err == nil) != (ret1 == nil) {
		return false
	}
	return err != nil || ret == want
}
```

## 3. Round trip and inverse

**Where:** Encode/Decode, Marshal/Unmarshal, Parse/String, Quote/Unquote,
Escape/Unescape, compress/decompress.

**Defects:** go-diff losing text when bytes and runes were mixed (applying
the diff must give back the text).

**How:** `Decode(Encode(x)) == x` over the domain the docs allow. When the
encoder is lossy by design, use the weaker
`Encode(Decode(Encode(x))) == Encode(x)`.

```go
// go2lean:spec Quote
// SpecQuoteRoundTrip: Unquote(Quote(s)) gives s back.
func SpecQuoteRoundTrip(s string, ret string) bool {
	back, err := Unquote(ret)
	return err == nil && back == s
}
```

## 4. Idempotence

**Where:** normalizers, cleaners, canonicalizers, formatters, case
converters, deduplication, sorting.

**Defects:** strcase's delimiter logic (`ToSnake(ToSnake(x)) != ToSnake(x)`
exposes it).

```go
// go2lean:spec ToSnake
// SpecToSnakeIdempotent: snake-casing a snake-cased string changes nothing.
func SpecToSnakeIdempotent(s string, ret string) bool {
	return ToSnake(ret) == ret
}
```

## 5. Invariants: range, length, sign, membership

**Where:** a result has a documented range or shape:
- a similarity in [0, 1];
- an index in [-1, len);
- a result no longer than its input;
- a width at most w;
- an output made of allowed characters only, such as a metric name.

**Defects:** the Sørensen-Dice similarity exceeding 1 (a float: run it with
`go2lean fuzz`); Prometheus unit names breaking the metric-name grammar.

```go
// go2lean:spec Truncate
// SpecTruncateWidth: the doc says the result fits in w columns.
func SpecTruncateWidth(s string, w int, tail string, ret string) bool {
	if w < 0 {
		return true
	}
	return StringWidth(ret) <= w
}
```

## 6. Algebraic relations: symmetry, reflexivity, antisymmetry, monotonicity, permutation invariance

**Where:** comparators, distances, similarity, set operations, anything
whose result should not depend on the order of its inputs.

**Defects:** natsort's `Compare` on inputs with different chunk counts
(`Compare(a, b)` and `Compare(b, a)` both true); the Prometheus
consistency check depending on label order.

```go
// go2lean:spec Compare
// SpecCompareStrict: a strict order never holds both ways, nor for equals.
func SpecCompareStrict(a, b string, ret bool) bool {
	return !(ret && Compare(b, a)) && !(a == b && ret)
}
```

## 7. Bytes against runes

**Where:** code that measures, indexes, truncates or compares text:
- `len(s)` next to `[]rune(s)`;
- `utf8.RuneCountInString`;
- string slicing by an index computed on runes.

**Defects:** fuzzy matching (`len(pattern)` against `len(runes)`); go-diff.

**How:** the same property as in 1 to 6, checked on multibyte input. The
search draws strings from the bytes the code compares against; seed a
multibyte constant (`"é"`, `"日本"`) in the spec when the code has none.
Or state the relation between the byte and rune views directly.

## 8. Termination and resource growth

**Where:** lexers, parsers, decoders and loops that advance by a
data-dependent amount; recursion on input structure; allocation sized
from a length field in the input.

**Defects:** the toml lexer looping on an unterminated table name; gjson's
exponential wildcard match. Resource exhaustion is the largest class in
the stdlib and x/ advisories.

**How:** search reports a run that runs out of fuel. On real Go, check
that the run ends within a time bound proportional to the input. A spec
can also bound a result's size by the input's size.

## 9. Entry-point no-panic on nil, empty and boundary inputs

**Where:** exported entry points, and zero-value receivers of exported
types. This is the weakest oracle, and it is the right one only where the
docs allow the input.

**Defects:** gin's `Context` with no engine; toml encoding a nil interface;
natsort's index out of range.

**How:** go2lean's template `no_panic` properties already cover this.
Before reporting, check that the docs allow the input, for example that a
nil receiver or a negative length is legal.

## 10. Determinism and side effects

**Where:** code that ranges over maps, reads the clock or randomness, or
takes an `io.Writer`.

**Defects:** go-redis mutating a map while ranging over it; tablewriter
printing to stdout instead of its writer.

**How:**
- call the function twice on the same input and compare;
- in a Go test, check that the given writer received all the output.

Map-order dependence needs real Go (`go2lean fuzz` or a test run many
times): the model fixes the order.
