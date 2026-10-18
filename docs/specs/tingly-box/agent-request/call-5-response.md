The modelled subset covers primitives, strings, structs/pointers with concrete field types, and slices/arrays of those — but it cannot inspect fields whose type is an opaque/interface/struct from an external package (here, `anthropic.BetaContentBlockParamUnion` and its `OfText *BetaTextBlockParam` field, and `anthropic.ImageBlockParam` with `Source` of an external type). Looking inside `b.OfText.Text` or `img.Source.OfURL.URL` produces unknown values, so any equality test against them is useless. I have to drop both specs.

```go
package request

// No spec-in-Go functions could be written for the modelled targets in
// this package: the relevant parameter types (anthropic.BetaContentBlockParamUnion,
// *anthropic.ImageBlockParam) are external-package structs whose fields are
// themselves external-package structs or pointers, which falls outside the
// modelled subset. Keeping any equality-style assertion against those fields
// would only return a vacuous true and never catch a real bug.
```