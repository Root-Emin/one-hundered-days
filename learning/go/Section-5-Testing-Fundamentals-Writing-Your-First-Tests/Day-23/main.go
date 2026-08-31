package catalog

import (
	"fmt"
	"strings"
)

// ============================================================
// DOMAIN
// ============================================================

type Product struct {
	Name     string
	Price    int
	Quantity int
}

// ============================================================
// CALCULATE TOTAL
// ============================================================

func CalculateTotal(products []Product) int {

	total := 0

	for _, product := range products {
		total += product.Price * product.Quantity
	}

	return total
}

// ============================================================
// FILTER PRODUCTS
// ============================================================

func FilterByName(products []Product, query string) []Product {

	query = strings.ToLower(query)

	var result []Product

	for _, product := range products {

		if strings.Contains(
			strings.ToLower(product.Name),
			query,
		) {
			result = append(result, product)
		}
	}

	return result
}

// ============================================================
// FORMAT PRICE
// ============================================================

func FormatPrice(price int) string {

	return fmt.Sprintf(
		"₺%.2f",
		float64(price)/100,
	)
}
