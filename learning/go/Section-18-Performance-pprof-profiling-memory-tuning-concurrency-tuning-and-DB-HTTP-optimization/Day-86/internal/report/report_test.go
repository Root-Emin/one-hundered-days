package report_test

import (
	"strings"
	"testing"

	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-86/internal/report"
)

// The test that makes the optimization safe: the fast path must produce
// exactly what the slow path produced, byte for byte. Without this, "faster"
// is indistinguishable from "wrong".
func TestFastMatchesSlow(t *testing.T) {
	for _, count := range []int{0, 1, 2, 17, 250} {
		items := report.Sample(count, 42)

		slow := report.RenderSlow(items)
		fast := report.RenderFast(items)

		if slow != fast {
			t.Fatalf("count=%d: outputs differ\nslow:\n%s\nfast:\n%s", count, slow, fast)
		}
	}
}

// Edge cases the SKU normaliser has to agree on, since the fast path replaced
// a regexp with a hand-written loop.
func TestSKUNormalisationMatches(t *testing.T) {
	skus := []string{
		"sku_0001/a",
		"---leading",
		"trailing---",
		"a//b__c",
		"ALREADYCLEAN",
		"1234",
		"",
		"...",
	}

	for _, sku := range skus {
		items := []report.Item{{SKU: sku, Name: "x", Quantity: 1, PriceCent: 100, Categories: []string{"a"}}}

		if slow, fast := report.RenderSlow(items), report.RenderFast(items); slow != fast {
			t.Errorf("sku %q: slow=%q fast=%q", sku, slow, fast)
		}
	}
}

func TestRenderIsSortedBySKU(t *testing.T) {
	items := report.Sample(50, 7)

	lines := strings.Split(strings.TrimSpace(report.RenderFast(items)), "\n")

	previous := ""

	for _, line := range lines[:len(lines)-1] {
		sku := strings.TrimSpace(line[:12])

		if sku < previous {
			t.Fatalf("output is not sorted: %q came after %q", sku, previous)
		}

		previous = sku
	}
}

// Rendering must not reorder the caller's slice - a surprise mutation is the
// kind of bug that only shows up three call sites away.
func TestRenderDoesNotMutateInput(t *testing.T) {
	items := report.Sample(20, 3)

	before := make([]report.Item, len(items))
	copy(before, items)

	report.RenderFast(items)

	for i := range items {
		if items[i].SKU != before[i].SKU {
			t.Fatalf("input reordered at index %d", i)
		}
	}
}

//
// BENCHMARKS - the before/after evidence
//
// Run: go test -run XXX -bench . -benchmem ./.../internal/report
//

var sink string

func benchmarkRender(b *testing.B, render func([]report.Item) string, count int) {
	items := report.Sample(count, 42)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// Assign to a package-level variable so the compiler cannot delete
		// the call as dead code.
		sink = render(items)
	}
}

func BenchmarkRenderSlow100(b *testing.B)  { benchmarkRender(b, report.RenderSlow, 100) }
func BenchmarkRenderFast100(b *testing.B)  { benchmarkRender(b, report.RenderFast, 100) }
func BenchmarkRenderSlow1000(b *testing.B) { benchmarkRender(b, report.RenderSlow, 1000) }
func BenchmarkRenderFast1000(b *testing.B) { benchmarkRender(b, report.RenderFast, 1000) }
