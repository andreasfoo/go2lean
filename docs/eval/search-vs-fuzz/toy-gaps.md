# Tool gaps found while preparing evaluation 5 (tool frozen; fix afterwards)

1. A spec calling a generic function of the package under test (Last[int])
   gets havoc for the call ("call toy.Last[int]: t0 is havoc
   (unmodeled-call)"), with and without -deps. Toy: bench/toy.
2. With -deps, Count(strings.NewReader(s)) == len(s) is refuted in the model
   but holds in Go (spurious, oracle_holds_in_real_run): io.ReadAll on a
   strings.Reader. Without -deps the reader is havoc. Toy: bench/toy.
