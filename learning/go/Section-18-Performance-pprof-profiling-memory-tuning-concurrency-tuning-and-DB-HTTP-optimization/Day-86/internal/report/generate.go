package report

import (
	"fmt"
	"math/rand"
)

// Sample builds a deterministic workload.
//
// Deterministic matters twice: a benchmark comparing two implementations must
// feed both the same data, and a profile taken on Tuesday must be comparable
// to one taken on Wednesday.
func Sample(count int, seed int64) []Item {
	source := rand.New(rand.NewSource(seed))

	categories := []string{"office", "electronics", "kitchen", "outdoor", "clearance", "fragile"}

	items := make([]Item, count)

	for i := range items {
		labelCount := 1 + source.Intn(3)

		labels := make([]string, labelCount)

		for j := range labels {
			labels[j] = categories[source.Intn(len(categories))]
		}

		items[i] = Item{
			SKU:        fmt.Sprintf("sku_%04d/%c", source.Intn(9999), 'a'+rune(source.Intn(26))),
			Name:       fmt.Sprintf("product %d", i),
			Quantity:   1 + source.Intn(9),
			PriceCent:  int64(100 + source.Intn(50000)),
			Categories: labels,
		}
	}

	return items
}
