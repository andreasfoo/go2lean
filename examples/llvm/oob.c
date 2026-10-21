// The LLVM line's first example: two functions with undefined behaviour
// and one without, for `go2lean llvm examples/llvm/oob.c`.
#include <stddef.h>
#include <stdint.h>

// Off by one: reads xs[n].
int64_t sum(const int64_t *xs, size_t n) {
  int64_t s = 0;
  for (size_t i = 0; i <= n; i++) s += xs[i];
  return s;
}

// Signed overflow when a + b does not fit.
int32_t avg(int32_t a, int32_t b) { return (a + b) / 2; }

// Correct: no undefined behaviour for any buffer and its length.
int64_t max_of(const int64_t *xs, size_t n) {
  int64_t m = 0;
  for (size_t i = 0; i < n; i++)
    if (xs[i] > m) m = xs[i];
  return m;
}
