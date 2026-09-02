package encode_test

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-87/internal/encode"
)

// Every implementation must produce identical bytes. Without this, "fewer
// allocations" and "different output" look the same from the benchmark.
func TestAllImplementationsAgree(t *testing.T) {
	for _, count := range []int{0, 1, 5, 100} {
		records := encode.Sample(count, 7)

		want := encode.EncodeNaive(records)

		if got := encode.EncodePrealloc(records); !bytes.Equal(got, want) {
			t.Errorf("count=%d: EncodePrealloc differs\n got: %q\nwant: %q", count, got, want)
		}

		if got := encode.AppendEncode(nil, records); !bytes.Equal(got, want) {
			t.Errorf("count=%d: AppendEncode differs", count)
		}

		if got := encode.BuildString(records); got != string(want) {
			t.Errorf("count=%d: BuildString differs", count)
		}

		if got := encode.NewEncoder().Encode(records); !bytes.Equal(got, want) {
			t.Errorf("count=%d: Encoder.Encode differs", count)
		}

		var buffer bytes.Buffer

		if _, err := encode.NewEncoder().WriteTo(&buffer, records); err != nil {
			t.Fatalf("WriteTo: %v", err)
		}

		if !bytes.Equal(buffer.Bytes(), want) {
			t.Errorf("count=%d: Encoder.WriteTo differs", count)
		}
	}
}

func TestQuotingMatchesFmt(t *testing.T) {
	// strconv.AppendQuote replaced %q; the escaping has to be identical.
	records := []encode.Record{{
		Level:   "INFO",
		Service: "orders",
		Message: "he said \"hi\"\tand left\n",
	}}

	want := encode.EncodeNaive(records)

	if got := encode.EncodePrealloc(records); !bytes.Equal(got, want) {
		t.Errorf("quoting differs\n got: %q\nwant: %q", got, want)
	}
}

// AppendEncode into a buffer that is already large enough must not allocate at
// all. This is the property the whole append-style API exists for, so it is
// worth asserting rather than assuming.
func TestAppendEncodeIntoAWarmBufferDoesNotAllocate(t *testing.T) {
	records := encode.Sample(100, 7)

	buffer := make([]byte, 0, encode.EstimateSize(records)*2)

	allocations := testing.AllocsPerRun(100, func() {
		buffer = encode.AppendEncode(buffer[:0], records)
	})

	if allocations != 0 {
		t.Errorf("AppendEncode into a warm buffer allocated %.0f times, want 0", allocations)
	}
}

// An allocation budget, enforced. A refactor that quietly reintroduces a
// per-record allocation fails here instead of six months later in a profile.
func TestAllocationBudget(t *testing.T) {
	records := encode.Sample(100, 7)

	// The encoder and its sink live outside the measured closure. Building
	// them inside would measure constructing a fresh pool every iteration,
	// which is exactly the thing a pool is supposed to avoid.
	encoder := encode.NewEncoder()

	var sink discardWriter

	cases := []struct {
		name    string
		budget  float64
		measure func()
	}{
		{
			name:    "EncodePrealloc",
			budget:  4, // the buffer, plus a little slack for the size estimate
			measure: func() { _ = encode.EncodePrealloc(records) },
		},
		{
			name:   "Encoder.WriteTo",
			budget: 1, // the pooled buffer, amortised to roughly nothing
			measure: func() {
				if _, err := encoder.WriteTo(&sink, records); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// AllocsPerRun runs the function b.N-style and returns the mean.
			// It pins the goroutine to one OS thread, so it is stable enough
			// to assert on.
			allocations := testing.AllocsPerRun(200, testCase.measure)

			if allocations > testCase.budget {
				t.Errorf("%s allocated %.1f times per call, budget is %.0f",
					testCase.name, allocations, testCase.budget)
			}
		})
	}
}

type discardWriter struct{ n int }

func (d *discardWriter) Write(p []byte) (int, error) {
	d.n += len(p)

	return len(p), nil
}

// A pooled buffer must never leak one call's data into the next. The failure
// mode is one user's response appearing in another user's, which is a security
// bug wearing a performance bug's clothes.
func TestPooledBuffersAreResetBetweenUses(t *testing.T) {
	encoder := encode.NewEncoder()

	secret := []encode.Record{{Level: "INFO", Service: "auth", Message: "token=SECRET-VALUE"}}
	other := []encode.Record{{Level: "INFO", Service: "orders", Message: "ok"}}

	if got := encoder.Encode(secret); !strings.Contains(string(got), "SECRET-VALUE") {
		t.Fatal("the first encode did not contain its own message")
	}

	for i := 0; i < 50; i++ {
		if got := encoder.Encode(other); strings.Contains(string(got), "SECRET") {
			t.Fatalf("iteration %d leaked the previous buffer's contents: %q", i, got)
		}
	}
}

// The pool has to survive concurrent use - that is the only situation where it
// is worth having.
func TestEncoderIsSafeUnderConcurrency(t *testing.T) {
	encoder := encode.NewEncoder()
	records := encode.Sample(20, 3)
	want := encode.EncodeNaive(records)

	var wg sync.WaitGroup

	for i := 0; i < 16; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for j := 0; j < 50; j++ {
				if got := encoder.Encode(records); !bytes.Equal(got, want) {
					t.Errorf("concurrent encode produced different output")

					return
				}
			}
		}()
	}

	wg.Wait()

	gets, allocations, _ := encoder.Stats()

	if gets == 0 {
		t.Fatal("the pool was never used")
	}

	// The whole point: far fewer buffers created than buffers requested.
	if allocations >= gets {
		t.Errorf("pool allocated %d buffers for %d gets - it is not reusing anything", allocations, gets)
	}
}

//
// BENCHMARKS - watch the allocs/op column, not just ns/op
//

var (
	byteSink   []byte
	stringSink string
)

func BenchmarkEncodeNaive(b *testing.B) {
	records := encode.Sample(100, 7)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		byteSink = encode.EncodeNaive(records)
	}
}

func BenchmarkEncodePrealloc(b *testing.B) {
	records := encode.Sample(100, 7)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		byteSink = encode.EncodePrealloc(records)
	}
}

// The same code without the size estimate, to show what preallocation alone is
// worth: append's doubling costs log2(n) allocations and copies everything
// each time it grows.
func BenchmarkEncodeNoPrealloc(b *testing.B) {
	records := encode.Sample(100, 7)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		byteSink = encode.AppendEncode(nil, records)
	}
}

func BenchmarkBuildString(b *testing.B) {
	records := encode.Sample(100, 7)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		stringSink = encode.BuildString(records)
	}
}

func BenchmarkAppendEncodeReusedBuffer(b *testing.B) {
	records := encode.Sample(100, 7)
	buffer := make([]byte, 0, encode.EstimateSize(records))

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		buffer = encode.AppendEncode(buffer[:0], records)
	}

	byteSink = buffer
}

func BenchmarkEncoderWriteTo(b *testing.B) {
	records := encode.Sample(100, 7)
	encoder := encode.NewEncoder()

	var sink discardWriter

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, err := encoder.WriteTo(&sink, records); err != nil {
			b.Fatal(err)
		}
	}
}

// Parallel is where a pool earns its keep - or fails to. sync.Pool keeps a
// per-P free list, so contention is low; a single shared buffer behind a mutex
// would serialise here.
func BenchmarkEncoderWriteToParallel(b *testing.B) {
	records := encode.Sample(100, 7)
	encoder := encode.NewEncoder()

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		var sink discardWriter

		for pb.Next() {
			if _, err := encoder.WriteTo(&sink, records); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// The counter-example: for a tiny buffer, the pool costs more than it saves.
func BenchmarkSmallBufferWithoutPool(b *testing.B) {
	records := encode.Sample(1, 7)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		byteSink = encode.EncodePrealloc(records)
	}
}

func BenchmarkSmallBufferWithPool(b *testing.B) {
	records := encode.Sample(1, 7)
	encoder := encode.NewEncoder()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		byteSink = encoder.Encode(records)
	}
}
