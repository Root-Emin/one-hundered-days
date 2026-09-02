package escape_test

import (
	"testing"

	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-87/internal/escape"
)

// These assertions are the point of the package: the measured behaviour, not
// the folklore. If a future Go release changes one of them, this test says so.
func TestEscapeBehaviour(t *testing.T) {
	cases := []struct {
		name    string
		want    float64
		measure func()
	}{
		{
			name:    "discarded pointer does not allocate",
			want:    0,
			measure: func() { _ = escape.NewPointEscapes(1, 2) },
		},
		{
			name: "retained pointer allocates",
			want: 1,
			measure: func() {
				// Storing it in a package-level variable is a use the
				// compiler cannot optimise away.
				escape.Sink = escape.NewPointEscapes(1, 2)
			},
		},
		{
			name:    "value constructor does not allocate",
			want:    0,
			measure: func() { _ = escape.NewPointStack(1, 2) },
		},
		{
			name:    "constant-size slice stays on the stack",
			want:    0,
			measure: func() { _ = escape.SumStackSlice() },
		},
		{
			name:    "small runtime-size slice also stays on the stack",
			want:    0,
			measure: func() { _ = escape.SumHeapSlice(64) },
		},
		{
			name:    "large runtime-size slice goes to the heap",
			want:    1,
			measure: func() { _ = escape.SumHeapSlice(1 << 20) },
		},
		{
			name:    "discarded closure does not allocate",
			want:    0,
			measure: func() { _ = escape.SumWithEscapingClosure([]int{1, 2, 3}) },
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := testing.AllocsPerRun(200, testCase.measure); got != testCase.want {
				t.Errorf("allocations = %.1f, want %.0f", got, testCase.want)
			}
		})
	}
}

// Both allocate the result string; the difference is CPU, and the benchmark is
// the only way to see it.
func BenchmarkFormatWithFmt(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		escape.Sink = escape.FormatWithFmt(4242)
	}
}

func BenchmarkFormatWithStrconv(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		escape.Sink = escape.FormatWithStrconv(4242)
	}
}
