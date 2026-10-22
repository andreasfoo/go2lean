# go2lean report: github.com/Masterminds/semver/v3

## 1. Confirmed bugs

### 1.1 A full wildcard under `^` (and `<=`) rejects releases with major > 0

- **Property:** `p_..._seqwildcardmatcheseveryrelease_sequence` (SeqWildcardMatchesEveryRelease, drives `Constraints.Check`).
- **Input:** a release version built from `segs = [2541104282491002651, 5394047589315793651, 8797659533360908600]`, so major = 2541104282491002651 (> 0). The wildcard form is picked by `sel = -2743846568614014354`.
- **Real Go run:** no panic. `Check` returned **false**, so the property fails in the real program. The model's `index_out_of_range` is not the failure here. The real run's `ret:false` is.
- **Why:** in the source, every wildcard form accepts any release except two operators that do not handle a full wildcard (`*`, `x`, `X`, or an empty version part):
  - `""` / `=` go through `constraintTildeOrEqual`, which calls `constraintTilde`, and `~*` both reach the special case `c.con == 0.0.0 && !c.minorDirty && !c.patchDirty -> true`. They accept.
  - `>= *` compares `v.Compare(0.0.0) >= 0`. It accepts.
  - **`^*`**: `parseConstraint` sets only `dirty = true` (minorDirty and patchDirty stay false) and `con = 0.0.0`. In `constraintCaret`, the branch `if c.con.Major() > 0 || c.minorDirty` is skipped. Then
    ```
    if c.con.Major() == 0 && v.Major() > 0 {
        return false, fmt.Errorf("%q does not have same major version as %q", v, c.orig)
    }
    ```
    rejects every version with major > 0. That contradicts the function's own header comment (`// ^*      -->  (any)`).
  - **`<= *`**: in `constraintLessThanEqual`, the dirty path `if v.Major() > c.con.Major() { return false, ... }` compares against `con = 0.0.0`. So `<= *` also rejects every version with major > 0. This contradicts the package doc, which says wildcards work for all operators and `*` is equivalent to `>= 0.0.0`.

  The failing version has major > 0, so the selected form must be one of these two. The fix is the same for both.
- **Suggested fix:** at the start of the dirty handling in `constraintCaret` (right after the `v.LessThan(c.con)` check) and in `constraintLessThanEqual` (right after `if !c.dirty { ... }`), short-circuit the full wildcard the same way `constraintTilde` already does:
  ```go
  // full wildcard (^*, <=*, ^x, ...): matches every version
  if c.dirty && !c.minorDirty && !c.patchDirty {
      return true, nil
  }
  ```
  (`^0` and `^0.0` are not affected: `parseConstraint` sets `minorDirty` or `patchDirty` for them.)
- A plain repro for maintainers: `c, _ := NewConstraint("^*"); c.Check(MustParse("1.0.0"))` returns `false`, and so does `NewConstraint("<= *")`.

### 1.2 Real panics that are not package bugs (the properties are too strong)

The real Go run reproduced each of these. In each one the panic needs an input that the package's public API cannot produce, or the function is documented to panic, so I do not report them as bugs:

| Property | Real panic | Why it is not a bug |
|---|---|---|
| `collection_less_nopanic` (i=-2166468501389848043) | index out of range | `Less(i, j)` is a `sort.Interface` method. Callers must pass `0 <= i, j < Len()`. |
| `collection_swap_nopanic` (i=MinInt64, j=-1) | index out of range | Same contract as `Less`. |
| `constraints_check_nopanic`, `constraints_validate_nopanic` | `index out of range [0] with length 0` on `cs.containsPre[i]` | The harness built a `Constraints` with groups but an empty `containsPre`. Both fields are unexported and always allocated together with length `len(ors)` in `NewConstraint`. A zero `Constraints{}` has no groups and returns false without panicking. |
| `constraints_string_nopanic`, `constraints_marshaltext_nopanic` | nil pointer dereference in `c.string()` | The diagnosis is `nil_opaque_input`: a nil `*constraint` inside the unexported `constraints` slice. `NewConstraint` never stores nil. |
| `mustparse_nopanic` | `panic: invalid semantic version` | `MustParse` is documented as "parses a given version and panics on error". Any unparsable string (for example `""`) panics by design. |
| `validateversion_nopanic` (m = `[]string{}`) | index out of range [1] | This is an internal helper. Its only caller passes `looseVersionRegex.FindStringSubmatch(v)`, which always has 9 elements. |

## 2. Spurious counterexamples

### 2.1 Tool (model) errors: the real run did not fail

For every one of the following, the real Go run behaved correctly. The model failed because it chose results for library calls it does not model (`regexp.(*Regexp).FindAllStringSubmatch` and `FindStringSubmatch`, `fmt.Fprintf`, `(*bytes.Buffer).String`, `strings.IndexFunc`), or because it built a `*Version` that `NewVersion` cannot return (`precondition_false`). The specs are fine. Re-running with `-deps` should remove these:

- **no_panic, regexp results unmodeled:** `NewConstraint`, `Constraints.UnmarshalText`, `NewVersion`, `coerceNewVersion`, `parseConstraint`, `rewriteRange`. The real runs returned ordinary errors or strings.
- **no_panic on `*Version` obtained from `NewVersion` (all `precondition_false`):** Compare, Equal, GreaterThan, GreaterThanEqual, LessThan, LessThanEqual, IncMajor/IncMajorE, IncMinor/IncMinorE, IncPatch/IncPatchE, Major, Minor, Patch, Prerelease, Metadata, Original, originalVPrefix, String, MarshalJSON, MarshalText, UnmarshalJSON, UnmarshalText, Scan, Value, SetMetadata, SetPrerelease.
- **Postconditions where the oracle holds in the real run:**
  - `SpecCollectionLessIsStrictOrder` (i=j=0): the real `Less(0,0)` is false, which is correct for a strict order.
  - `SpecNewRoundTripsThroughStrictParse`: the real string `18446744073709551608.1.18446744073709551614` parses back fine.
  - `SpecVersionSetMetadataKeepsPrecedence`: holds in the real run.
  - `SpecVersionSetPrereleaseRoundTrip`: the real run correctly rejects `.x` with `invalid prerelease string`.
- **Sequences where the real run returned true:** SeqCaretMatchesDocumentedRange, SeqTildeMatchesDocumentedRange, SeqConstraintOpsComplementary, SeqConstraintOpsComplementaryWithPrerelease, SeqConstraintsTextRoundTrip, SeqValidateAgreesWithCheck.

### 2.2 Properties that are wrong or too strong, and how I would restate them

The six properties in 1.2 come from the generated no_panic family, not from the spec file. I would restate them with the preconditions the API guarantees:

- `Collection.Less` / `Collection.Swap`: no panic for `0 <= i, j < c.Len()` and non-nil elements.
- `Constraints.Check` / `Validate` / `String` / `MarshalText`: no panic for any `Constraints` obtained from a successful `NewConstraint(s)` (or `UnmarshalText`), with any value of `IncludePrerelease`.
- `MustParse`: no panic whenever `NewVersion(s)` returns a nil error. Equivalently: it panics exactly when `NewVersion` fails.
- `validateVersion`: no panic for `m = looseVersionRegex.FindStringSubmatch(v)` with `m != nil`, or drop it as an internal helper.

A spec-in-Go property that holds only vacuously:

- `SpecStrictNewVersionAgreesWithNewVersion` was reported `exhausted_assuming`, but "**0 of them got past the spec's guards**". The exhaustive domain (`""`, `"a"`, small mined literals) never contains a well-formed `X.Y.Z` string, so nothing was checked. It needs a generator-shaped input: three small numeric segments plus an optional prerelease and metadata built from a selector, with the string assembled inside the spec. That way the guard passes on the enumerated domain.

## 3. Properties that held

**Certified by the exhaustive engine:**
- `compareSegment` no_panic: **proved** for every input over the full width of both `uint64` parameters, with no havoc and no assumptions. This is the only unconditional result.
- `isX` no_panic: **exhausted** over 49 inputs (boundary values, mined literals, `""`, `"a"`), with no havoc. It is complete for that finite domain only. The function is a constant switch, so this is effectively total.

**Exhausted, but only under havoc or assumptions (bounded evidence, not proofs):**
- `containsOnly` (256 inputs), `validateMetadata` (51), `validatePrerelease` (80): `exhausted_havoc`. Unmodeled calls (`strings.IndexFunc`, `strings.Split`, `strings.ContainsRune`) were replaced by three havoc values each.
- `StrictNewVersion` (171 inputs) and `comparePrerelease` (2116 inputs): `exhausted_assuming`, each with one under-approximation.
- `SpecStrictNewVersionAgreesWithNewVersion`: this one is vacuous (see 2.2). Do not count it as evidence.

**Only "no counterexample found" by random search:**
- No-panic: `Collection.Len`, `New`, `constraint.check`, `constraint.string`, `constraintCaret`, `constraintGreaterThan`, `constraintGreaterThanEqual`, `constraintLessThan`, `constraintLessThanEqual`, `constraintNotEqual`, `constraintTilde`, `constraintTildeOrEqual`.
- Postconditions: `SpecMustParseStringIsStrict`, `SpecVersionComparePrecedence`, `SpecVersionEqualIgnoresMetadataAndIsSymmetric`, `SpecVersionIncMajorENext`, `SpecVersionIncMinorENext`, `SpecVersionIncPatchENext`, `SpecVersionMarshalJSONRoundTrip`, `SpecVersionMarshalTextRoundTrip`, `SpecVersionValueScanRoundTrip`.

## Revised specs

I am not including a revised `go2lean_specs_test.go` in this round:

- The confirmed wildcard counterexample is a real package bug. The spec should keep exercising the full-wildcard `^` and `<=` forms so the fix can be verified.
- Every other spurious result is a model limitation (unmodeled `regexp`/`fmt`/`strings` calls), not a wrong spec. The fix there is to run with `-deps`, not to weaken the specs.
- The over-strong properties in 2.2 belong to the generated no_panic family; their restatements are given above in prose.
- The only spec-file change I would make is to rebuild `SpecStrictNewVersionAgreesWithNewVersion` around a generated `X.Y.Z[-pre][+meta]` string so the exhaustive domain gets past its guard. The previous spec text is not in front of me, and a replacement file would overwrite all the other specs. So I am raising this as a recommendation rather than sending a partial file.
