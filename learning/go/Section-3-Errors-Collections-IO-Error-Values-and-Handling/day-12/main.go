package main

import (
	"fmt"
)

// ============================================================
// PRODUCT
// ============================================================

type Product struct {
	ID       int
	Name     string
	Category string
	Price    float64
}

// ============================================================
// CART ITEM
// ============================================================

type CartItem struct {
	ProductID int
	Quantity  int
}

// ============================================================
// ADD PRODUCT TO CART
// ============================================================

// addToCart slice'a yeni bir CartItem ekler.
//
// append kullandığımız için yeni slice'ı return ediyoruz.
//
// Bu önemli:
// append sonucunda Go yeni bir backing array
// oluşturabilir. Bu yüzden caller'ın returned slice'ı
// alması gerekir.
func addToCart(cart []CartItem, productID int, quantity int) []CartItem {

	item := CartItem{
		ProductID: productID,
		Quantity:  quantity,
	}

	return append(cart, item)
}

// ============================================================
// FIND PRODUCT
// ============================================================

// findProduct map üzerinden Product bulur.
//
// Başarılı:
//
//	product, true
//
// Bulunamadı:
//
//	Product{}, false
func findProduct(
	products map[int]Product,
	productID int,
) (Product, bool) {

	product, ok := products[productID]

	return product, ok
}

// ============================================================
// CHECK INVENTORY
// ============================================================

// inventory map'inde ürünün stok miktarını kontrol ediyoruz.
func checkInventory(
	inventory map[int]int,
	productID int,
	quantity int,
) bool {

	stock, ok := inventory[productID]

	if !ok {
		return false
	}

	return stock >= quantity
}

// ============================================================
// REDUCE INVENTORY
// ============================================================

// Map'ler reference semantics benzeri davranış gösterdiği için
// map'i fonksiyona gönderdiğimizde içeride yaptığımız değişiklik
// caller tarafından görülür.
func reduceInventory(
	inventory map[int]int,
	productID int,
	quantity int,
) {

	inventory[productID] -= quantity
}

// ============================================================
// DISCOUNT
// ============================================================

// Slice'ı fonksiyona value olarak geçiriyoruz.
//
// Ancak slice header'ın gösterdiği backing array paylaşılabilir.
//
// Bu yüzden items[index].Price değiştirildiğinde,
// caller'ın slice'ı da etkilenebilir.
func applyDiscount(
	products []Product,
	discountRate float64,
) {

	for i := range products {
		products[i].Price *= 1 - discountRate
	}
}

// ============================================================
// COPY PRODUCTS
// ============================================================

// Ürün listesinin bağımsız bir kopyasını oluşturuyoruz.
func cloneProducts(products []Product) []Product {

	cloned := make([]Product, len(products))

	copy(cloned, products)

	return cloned
}

// ============================================================
// PRINT PRODUCTS
// ============================================================

func printProducts(products []Product) {

	fmt.Println("PRODUCT CATALOG")

	for index, product := range products {
		fmt.Printf(
			"%d -> %s | %.2f TL\n",
			index,
			product.Name,
			product.Price,
		)
	}
}

// ============================================================
// PRINT INVENTORY
// ============================================================

func printInventory(inventory map[int]int) {

	fmt.Println("INVENTORY")

	for productID, stock := range inventory {

		fmt.Printf(
			"Product ID: %d | Stock: %d\n",
			productID,
			stock,
		)
	}
}

// ============================================================
// PRINT CART
// ============================================================

func printCart(
	cart []CartItem,
	products map[int]Product,
) {

	fmt.Println("SHOPPING CART")

	for _, item := range cart {

		product, ok := products[item.ProductID]

		if !ok {
			fmt.Printf(
				"Unknown product ID: %d\n",
				item.ProductID,
			)

			continue
		}

		total := product.Price * float64(item.Quantity)

		fmt.Printf(
			"%s | Quantity: %d | Total: %.2f TL\n",
			product.Name,
			item.Quantity,
			total,
		)
	}
}

// ============================================================
// MAIN
// ============================================================

func main() {

	// ========================================================
	// 1. SLICE LITERAL
	// ========================================================

	products := []Product{
		{
			ID:       100,
			Name:     "Mechanical Keyboard",
			Category: "Keyboard",
			Price:    2500,
		},
		{
			ID:       200,
			Name:     "Wireless Mouse",
			Category: "Mouse",
			Price:    1200,
		},
		{
			ID:       300,
			Name:     "4K Monitor",
			Category: "Monitor",
			Price:    8500,
		},
		{
			ID:       400,
			Name:     "Gaming Headset",
			Category: "Audio",
			Price:    3200,
		},
	}

	fmt.Println("Number of products:", len(products))
	fmt.Println("Product capacity:", cap(products))

	fmt.Println()

	// ========================================================
	// 2. APPEND
	// ========================================================

	products = append(products, Product{
		ID:       500,
		Name:     "Webcam",
		Category: "Camera",
		Price:    1800,
	})

	fmt.Println("After append:")
	fmt.Println("Number of products:", len(products))

	fmt.Println()

	// ========================================================
	// 3. SLICE
	// ========================================================

	featuredProducts := products[0:3]

	fmt.Println("FEATURED PRODUCTS")

	for _, product := range featuredProducts {

		fmt.Printf(
			"%s | %.2f TL\n",
			product.Name,
			product.Price,
		)
	}

	fmt.Println()

	// ========================================================
	// 4. MAP CREATION
	// ========================================================

	productsByID := make(map[int]Product)

	for _, product := range products {
		productsByID[product.ID] = product
	}

	// ========================================================
	// 5. MAP LOOKUP
	// ========================================================

	product, ok := findProduct(productsByID, 300)

	if ok {
		fmt.Println(
			"Found product:",
			product.Name,
		)
	} else {
		fmt.Println("Product not found")
	}

	fmt.Println()

	// ========================================================
	// 6. MAP LOOKUP - PRODUCT DOES NOT EXIST
	// ========================================================

	_, ok = findProduct(productsByID, 999)

	if !ok {
		fmt.Println(
			"Product 999 does not exist",
		)
	}

	fmt.Println()

	// ========================================================
	// 7. INVENTORY MAP
	// ========================================================

	inventory := map[int]int{
		100: 10,
		200: 25,
		300: 5,
		400: 8,
		500: 15,
	}

	printInventory(inventory)

	fmt.Println()

	// ========================================================
	// 8. CHECK INVENTORY
	// ========================================================

	if checkInventory(inventory, 300, 2) {
		fmt.Println(
			"Enough stock for product 300",
		)
	} else {
		fmt.Println(
			"Not enough stock for product 300",
		)
	}

	fmt.Println()

	// ========================================================
	// 9. SHOPPING CART
	// ========================================================

	var cart []CartItem

	cart = addToCart(cart, 100, 2)
	cart = addToCart(cart, 300, 1)
	cart = addToCart(cart, 400, 1)

	printCart(cart, productsByID)

	fmt.Println()

	// ========================================================
	// 10. REDUCE INVENTORY
	// ========================================================

	reduceInventory(
		inventory,
		100,
		2,
	)

	fmt.Println(
		"Inventory after purchasing product 100:",
		inventory[100],
	)

	fmt.Println()

	// ========================================================
	// 11. MAP UPDATE
	// ========================================================

	inventory[200] = 30

	fmt.Println(
		"Updated stock for product 200:",
		inventory[200],
	)

	fmt.Println()

	// ========================================================
	// 12. MAP DELETE
	// ========================================================

	delete(inventory, 500)

	_, ok = inventory[500]

	if !ok {
		fmt.Println(
			"Product 500 removed from inventory",
		)
	}

	fmt.Println()

	// ========================================================
	// 13. MAP RANGE
	// ========================================================

	fmt.Println("INVENTORY ITERATION")

	for productID, stock := range inventory {

		fmt.Printf(
			"Product %d -> Stock %d\n",
			productID,
			stock,
		)
	}

	fmt.Println()

	// ========================================================
	// 14. SLICE RANGE
	// ========================================================

	fmt.Println("PRODUCT ITERATION")

	for index, product := range products {

		fmt.Printf(
			"Index %d -> %s\n",
			index,
			product.Name,
		)
	}

	fmt.Println()

	// ========================================================
	// 15. COPY
	// ========================================================

	productSnapshot := cloneProducts(products)

	// Snapshot üzerinde değişiklik yapıyoruz.
	productSnapshot[0].Price = 999

	fmt.Println(
		"Original product price:",
		products[0].Price,
	)

	fmt.Println(
		"Snapshot product price:",
		productSnapshot[0].Price,
	)

	fmt.Println()

	// ========================================================
	// 16. SHARED BACKING ARRAY
	// ========================================================

	originalProducts := []Product{
		{
			ID:    1,
			Name:  "Laptop",
			Price: 50000,
		},
		{
			ID:    2,
			Name:  "Tablet",
			Price: 20000,
		},
	}

	featured := originalProducts[:1]

	featured[0].Price = 45000

	fmt.Println(
		"Original laptop price:",
		originalProducts[0].Price,
	)

	fmt.Println(
		"Featured laptop price:",
		featured[0].Price,
	)

	fmt.Println()

	// ========================================================
	// 17. SAFE INDEPENDENT COPY
	// ========================================================

	safeFeatured := cloneProducts(
		originalProducts[:1],
	)

	safeFeatured[0].Price = 40000

	fmt.Println(
		"Original laptop price:",
		originalProducts[0].Price,
	)

	fmt.Println(
		"Copied featured laptop price:",
		safeFeatured[0].Price,
	)

	fmt.Println()

	// ========================================================
	// 18. DISCOUNT
	// ========================================================

	fmt.Println("BEFORE DISCOUNT")

	printProducts(products)

	fmt.Println()

	applyDiscount(products, 0.10)

	fmt.Println("AFTER 10% DISCOUNT")

	printProducts(products)
}
