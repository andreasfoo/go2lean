# Rejected candidates

Candidates looked at that do not qualify (or could not be confirmed), with the reason.

- **sergi/go-diff 3202072e** ("fix panic: slice bounds out of range"): the panic is in methods of `*DiffMatchPatch` (`DiffCharsToLines`, `PatchMake`); the plain helper `intArrayToString` was only removed.
- **yuin/goldmark 883918a8** `URLEscape(v []byte, resolveReference bool) []byte`: fix (`n < limit` before `v[n:]`) only avoids a panic when `cap(v)` lets the unfixed `v[i:i+u8len]` read past `len(v)`; with `cap == len` both versions panic at that earlier slice. Capacity-dependent in the wrong direction.
- **pierrec/lz4 02ed8a49 / 1fa8617f** `decodeBlock(dst, src []byte) int`, `decodeBlockGo`: the decoder `recover()`s every panic into an error code, and the fixes cap slices to stop out-of-bounds *writes* within capacity; no panic escapes either version.
- **miekg/dns c37add8d** `idn.ToPunycode("")` / `FromPunycode("")`: confirmed panic (`s[len(s)-1]`, index -1) before and none after, but the functions call `dns.SplitDomainName` from another package (github.com/miekg/dns), which the "same package or stdlib only" rule excludes. (Its reproducer is not included.)
- **miekg/dns ccd41ffa** `typeBitMapLen([]uint16) int`: new helper introduced by the fix; the panic was in `NSEC3.pack` (method).
- **charmbracelet/x 3a818a52** `ansi.SetProgress(int)`: clamps the percentage; never panicked.
- **golang/net 5a3baee3** `appendPrefixedInt`: the overflow/panic fix is in the method `readPrefixedIntWithByte`; the plain function did not panic.
- **valyala/fasthttp 42f83c60** `round2(n int) int`: overflow made it return 0 (panic happened in callers), no panic inside the function.
- **valyala/fasthttp d0f2727a** `AppendUint`, `stripLeadingSlashes`: only comments added next to explicit panics, which remain.
- **valyala/fasthttp d0dfbd44** `peekRawHeader`, `removeNewLines`, `isOnlyCRLF`: constant renames only in these functions.
- **huandu/xstrings 32b28481** `Slice`: the commit *adds* an explicit `panic("out of range")`; opposite direction.
- **iancoleman/strcase d8275f25, f2ad438e** `ToSnake`: logic changes to acronym handling, no panic before.
- **gookit/color 9bc305ff** `C256ToRgb(uint8) []uint8`: probed all 256 inputs on the parent, none panics (the change is a correctness fix).
- **gookit/goutil e000f22a** `strutil.Substr` large length: the parent already contains the f028814a clamp, so the overflow gave a wrong result ("") rather than a panic.
- **charmbracelet/x 5a7fd322, 30996771** `ansi.TruncateLeft` / `ansi.Truncate`: semantic edge-case fixes, no panic.
- **gookit/goutil 079a35db, 3dfc8583** `strutil.Repeat`: behaviour change for times <= 1, no panic.
- **yuin/goldmark 97df31c2** `util.URLEscape` ("Fix #245 - 10"): changes a clamp (`>=` to `>`); output fix, no panic difference found.
- **knqyf263/go-deb-version c6345661** `compare(v1, v2 string) int`: ordering bug fix; `strings1.get(i)` is already bounds-safe.
- **olekukonko/tablewriter a18d3c87** `Title`, **mattn/go-shellwords ac2109d8** `isEnv`, **tidwall/sjson 051af16d** `mustMarshalString`, **gookit/color 259f4047** `WrapTag`, **gosimple/slug 6732d2a7** `smartTruncate`, **hbollon/go-edlib 73370c0b** `LCS`: behaviour/correctness changes with no panic in the parent.
- **ulikunitz/xz 687a4cb5** `putUvarint(p []byte, x uint64) int` (`x >= 80` -> `x >= 0x80`): an encoding bug; it only "panics" if the caller passes an exactly-sized buffer (e.g. len 1, x = 100). Not a panic fix in intent, left out.
- **valyala/fasthttp 42f83c60, d0f2727a, d0dfbd44**, **charmbracelet/x 3a818a52**: see above; **buger/jsonparser 817f639, 63af0e0**: panics fixed in `findKeyStart` (two results) and the variadic `Set`. **olekukonko/tablewriter d9cbf0c**, **russross/blackfriday 289c704, d28de22, 232d06c, ca82b8d**: panic fixes in methods.
- **nyaruka/phonenumbers be30c4c, d882292, 01360f7**: fixed functions take `*PhoneNumber`, return `([]string, error)`, or have many parameters of other types.
- **andybalholm/brotli 45f5d9b**, **golang/text 305da72**: fixes in functions with pointer/struct parameters or methods.
- **gookit/color ef0eb1d** `C256ToRgbV1`: uint8 arithmetic wrap-around (wrong colour), no panic. **docker/go-units a09cd47** `CustomSize`: float64 parameters.
- **golang/text 967b8f61** bidi `ReverseString`/`AppendReverse`: replaced `panic("unimplemented")` stubs with an implementation (feature, not a bug fix); not pursued.

## Differential probing sweep

As a second pass, every small commit (<= 30 changed non-test lines) touching a qualifying function in the cloned repos (1388 commit/function items in 61 repos, minus gjson and lancet which already had 4 pairs) was probed: a generated test called the function with up to 25,000 combinations from fixed input pools on the parent and the fix commit, and compared the panicking inputs. Apart from re-finding pairs already listed, it found the govalidator `IsDivisibleBy` and lz4 `Put` pairs. Its other hits were false positives:
- **miekg/dns c38aeeb3** `idn.adapt` and **tdewolff/minify 7e35af5f** `hexadecimalNumber`: the fix changed the parameter types (`rune` -> `int`, extra `prec int`), so the "fixed" inputs differ only in how they are typed; both versions panic.
- Packages that import os, os/exec, syscall, net, path/filepath or unsafe were skipped for safety (this covers most of miekg/dns and valyala/fasthttp). gomarkdown/markdown was stopped before it finished.
