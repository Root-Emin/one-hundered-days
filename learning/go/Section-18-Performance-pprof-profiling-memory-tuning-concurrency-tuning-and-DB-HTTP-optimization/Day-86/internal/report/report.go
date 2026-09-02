// Package report is the workload we profile: rendering a plain-text invoice
// report from a list of line items.
//
// There are two implementations of the same function. RenderSlow is written
// the way code actually gets written under deadline - correct, readable, and
// quietly wasteful. RenderFast is what the profile says to write instead.
//
// Both are kept, and both are tested against each other, because an
// optimization you cannot prove produces identical output is not an
// optimization - it is a rewrite with a bug in it.
package report

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Item is one line of the invoice.
type Item struct {
	SKU        string
	Name       string
	Quantity   int
	PriceCent  int64
	Categories []string
}

// skuPattern normalises SKUs. Compiling a regexp is expensive - roughly
// microseconds - which is nothing once, and everything inside a loop.
var skuPattern = regexp.MustCompile(`[^A-Za-z0-9]+`)

//
// THE SLOW VERSION
//

// RenderSlow is the version the profile complains about. Every problem in it
// is a normal thing to write:
//
//  1. `output += ...` in a loop - each += allocates a new string and copies
//     everything written so far, so rendering n lines does O(n²) copying.
//  2. regexp.MustCompile inside the loop - recompiling the same pattern once
//     per item.
//  3. fmt.Sprintf for simple concatenation - it parses the format string and
//     boxes every argument into an interface at runtime.
//  4. A map rebuilt per item instead of once.
func RenderSlow(items []Item) string {
	output := ""

	// Sort a copy so the caller's slice is untouched.
	sorted := []Item{}

	for _, item := range items {
		sorted = append(sorted, item)
	}

	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].SKU < sorted[j].SKU
	})

	var total int64

	for _, item := range sorted {
		// Recompiled on every single iteration.
		pattern := regexp.MustCompile(`[^A-Za-z0-9]+`)
		sku := strings.ToUpper(pattern.ReplaceAllString(item.SKU, "-"))

		lineTotal := item.PriceCent * int64(item.Quantity)
		total += lineTotal

		// A fresh map per item, thrown away at the end of the iteration.
		categories := map[string]bool{}

		for _, category := range item.Categories {
			categories[category] = true
		}

		labels := []string{}

		for category := range categories {
			labels = append(labels, category)
		}

		sort.Strings(labels)

		output += fmt.Sprintf("%-12s %-24s %3d x %8s = %10s  [%s]\n",
			sku, item.Name, item.Quantity,
			formatCentsSlow(item.PriceCent), formatCentsSlow(lineTotal),
			strings.Join(labels, ","))
	}

	output += fmt.Sprintf("%-12s %-24s %3s   %8s   %10s\n", "", "TOTAL", "", "", formatCentsSlow(total))

	return output
}

func formatCentsSlow(cents int64) string {
	return fmt.Sprintf("%d.%02d", cents/100, cents%100)
}

//
// THE FAST VERSION
//

// RenderFast produces byte-identical output. The changes are all mechanical:
//
//  1. strings.Builder with Grow - one buffer, one allocation, no copying.
//  2. The regexp is compiled once, at package level.
//  3. strconv.AppendInt instead of fmt.Sprintf - no format parsing, no
//     interface boxing, and it appends into the existing buffer.
//  4. The scratch slice for categories is allocated once and reused.
//
// None of this is clever. That is the point: most real wins are the removal of
// per-iteration work, not a better algorithm.
func RenderFast(items []Item) string {
	sorted := make([]Item, len(items))
	copy(sorted, items)

	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].SKU < sorted[j].SKU
	})

	var builder strings.Builder

	// ~72 bytes per line, measured from the output. Being roughly right
	// removes almost all of the growth reallocations; being exactly right is
	// not worth the effort.
	builder.Grow(len(items)*72 + 72)

	var (
		total   int64
		labels  = make([]string, 0, 8)
		scratch = make([]byte, 0, 64)
	)

	for _, item := range sorted {
		lineTotal := item.PriceCent * int64(item.Quantity)
		total += lineTotal

		labels = labels[:0]
		labels = appendUnique(labels, item.Categories)

		sort.Strings(labels)

		scratch = scratch[:0]
		scratch = appendUpperNormalised(scratch, item.SKU)

		writePadded(&builder, string(scratch), 12)
		builder.WriteByte(' ')
		writePadded(&builder, item.Name, 24)
		builder.WriteByte(' ')
		writeRightPadded(&builder, strconv.Itoa(item.Quantity), 3)
		builder.WriteString(" x ")
		writeRightPadded(&builder, formatCents(item.PriceCent), 8)
		builder.WriteString(" = ")
		writeRightPadded(&builder, formatCents(lineTotal), 10)
		builder.WriteString("  [")

		for i, label := range labels {
			if i > 0 {
				builder.WriteByte(',')
			}

			builder.WriteString(label)
		}

		builder.WriteString("]\n")
	}

	writePadded(&builder, "", 12)
	builder.WriteByte(' ')
	writePadded(&builder, "TOTAL", 24)
	builder.WriteByte(' ')
	writePadded(&builder, "", 3)
	builder.WriteString("   ")
	writePadded(&builder, "", 8)
	builder.WriteString("   ")
	writeRightPadded(&builder, formatCents(total), 10)
	builder.WriteByte('\n')

	return builder.String()
}

// formatCents avoids fmt for the hottest string in the report.
func formatCents(cents int64) string {
	buffer := make([]byte, 0, 20)
	buffer = strconv.AppendInt(buffer, cents/100, 10)
	buffer = append(buffer, '.')

	remainder := cents % 100
	if remainder < 10 {
		buffer = append(buffer, '0')
	}

	buffer = strconv.AppendInt(buffer, remainder, 10)

	return string(buffer)
}

// appendUpperNormalised is the loop-free equivalent of the regexp: replace any
// run of non-alphanumerics with a single '-', and upper-case the rest.
//
// A regexp is the right tool for a pattern that changes. For a rule this
// fixed, a hand-written loop is both faster and clearer about what it does.
func appendUpperNormalised(dst []byte, value string) []byte {
	pendingSeparator := false

	for i := 0; i < len(value); i++ {
		c := value[i]

		switch {
		case c >= 'a' && c <= 'z':
			if pendingSeparator {
				dst = append(dst, '-')
			}

			pendingSeparator = false
			dst = append(dst, c-'a'+'A')

		case (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9'):
			if pendingSeparator {
				dst = append(dst, '-')
			}

			pendingSeparator = false
			dst = append(dst, c)

		default:
			pendingSeparator = true
		}
	}

	// A leading or trailing run of separators still produces one "-", exactly
	// as the regexp's ReplaceAllString does. This is the case the equivalence
	// test caught.
	if pendingSeparator {
		dst = append(dst, '-')
	}

	return dst
}

func appendUnique(dst []string, values []string) []string {
	// For the handful of categories a line item has, a linear scan beats a
	// map: no hashing and no allocation.
	for _, value := range values {
		found := false

		for _, existing := range dst {
			if existing == value {
				found = true

				break
			}
		}

		if !found {
			dst = append(dst, value)
		}
	}

	return dst
}

const spaces = "                                "

func writePadded(builder *strings.Builder, value string, width int) {
	builder.WriteString(value)

	if pad := width - len(value); pad > 0 {
		builder.WriteString(spaces[:pad])
	}
}

func writeRightPadded(builder *strings.Builder, value string, width int) {
	if pad := width - len(value); pad > 0 {
		builder.WriteString(spaces[:pad])
	}

	builder.WriteString(value)
}
