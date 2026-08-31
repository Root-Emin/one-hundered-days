package main

import (
	"errors"
	"fmt"
)

// ============================================================
// 1. SENTINEL ERRORS
// ============================================================

// Uygulamanın belirli hata durumlarını temsil eden sabit error'lar.
//
// errors.New bize bir error değeri oluşturur.
// Daha sonra errors.Is ile bu hatayı kontrol edebiliriz.
var (
	ErrUserNotFound      = errors.New("user not found")
	ErrProductNotFound   = errors.New("product not found")
	ErrInsufficientStock = errors.New("insufficient stock")
)

// ============================================================
// 2. CUSTOM ERROR TYPE
// ============================================================

// ValidationError özel bir hata tipidir.
//
// Sadece "validation failed" demek yerine,
// hangi field'ın ve hangi value'nun hatalı olduğunu
// programın taşımasına izin verir.
type ValidationError struct {
	Field string
	Value string
}

// Error() methodu sayesinde ValidationError,
// Go'nun error interface'ini implement eder.
func (e *ValidationError) Error() string {
	return fmt.Sprintf(
		"validation failed: field=%s value=%q",
		e.Field,
		e.Value,
	)
}

// ============================================================
// 3. DATA STRUCTURES
// ============================================================

type User struct {
	ID   int
	Name string
}

type Product struct {
	ID    int
	Name  string
	Stock int
}

type Order struct {
	UserID    int
	ProductID int
	Quantity  int
}

// ============================================================
// 4. USER LOOKUP
// ============================================================

// findUser bir User ve error döndürüyor.
//
// Başarılı:
//
//	User, nil
//
// Başarısız:
//
//	User{}, ErrUserNotFound
//
// Burada error'ı fonksiyonun kendisi handle etmiyor.
// Hatayı caller'a gönderiyor.
func findUser(userID int) (User, error) {
	if userID <= 0 {
		return User{}, &ValidationError{
			Field: "userID",
			Value: fmt.Sprintf("%d", userID),
		}
	}

	// Basitlik amacıyla sadece ID 1'in var olduğunu varsayıyoruz.
	if userID != 1 {
		return User{}, ErrUserNotFound
	}

	return User{
		ID:   1,
		Name: "Emin",
	}, nil
}

// ============================================================
// 5. PRODUCT LOOKUP
// ============================================================

func findProduct(productID int) (Product, error) {
	if productID <= 0 {
		return Product{}, &ValidationError{
			Field: "productID",
			Value: fmt.Sprintf("%d", productID),
		}
	}

	// Sadece ID 100'ün var olduğunu varsayıyoruz.
	if productID != 100 {
		return Product{}, ErrProductNotFound
	}

	return Product{
		ID:    100,
		Name:  "Go Programming Book",
		Stock: 5,
	}, nil
}

// ============================================================
// 6. STOCK CHECK
// ============================================================

// checkStock stok yeterli mi kontrol ediyor.
//
// Burada yine (T, error) yerine sadece error dönüyoruz.
//
// Çünkü başarılı durumda bize ekstra bir veri lazım değil.
//
// nil     -> işlem başarılı
// error   -> işlem başarısız
func checkStock(product Product, quantity int) error {
	if quantity <= 0 {
		return &ValidationError{
			Field: "quantity",
			Value: fmt.Sprintf("%d", quantity),
		}
	}

	if product.Stock < quantity {
		return ErrInsufficientStock
	}

	return nil
}

// ============================================================
// 7. ORDER CREATION
// ============================================================

// createOrder bütün işlemleri birleştiren üst seviye fonksiyon.
//
// Burada özellikle FAIL FAST yaklaşımını kullanıyoruz.
//
// Herhangi bir işlem hata verirse:
//
//	if err != nil {
//	    return ...
//	}
//
// diyerek hemen çıkıyoruz.
func createOrder(userID int, productID int, quantity int) (Order, error) {

	// --------------------------------------------------------
	// STEP 1: USER BUL
	// --------------------------------------------------------

	user, err := findUser(userID)

	if err != nil {
		// Burada hatayı olduğu gibi göndermek yerine
		// context ekliyoruz.
		//
		// %w sayesinde orijinal error kaybolmuyor.
		return Order{}, fmt.Errorf(
			"failed to find user: %w",
			err,
		)
	}

	// --------------------------------------------------------
	// STEP 2: PRODUCT BUL
	// --------------------------------------------------------

	product, err := findProduct(productID)

	if err != nil {
		return Order{}, fmt.Errorf(
			"failed to find product: %w",
			err,
		)
	}

	// --------------------------------------------------------
	// STEP 3: STOCK KONTROLÜ
	// --------------------------------------------------------

	err = checkStock(product, quantity)

	if err != nil {
		return Order{}, fmt.Errorf(
			"failed to check stock: %w",
			err,
		)
	}

	// --------------------------------------------------------
	// STEP 4: ORDER OLUŞTUR
	// --------------------------------------------------------

	order := Order{
		UserID:    user.ID,
		ProductID: product.ID,
		Quantity:  quantity,
	}

	return order, nil
}

// ============================================================
// 8. MAIN
// ============================================================

func main() {

	// --------------------------------------------------------
	// SUCCESSFUL ORDER
	// --------------------------------------------------------

	order, err := createOrder(1, 100, 2)

	if err != nil {
		handleError(err)
		return
	}

	fmt.Println("Order created successfully:")
	fmt.Println(order)

	// --------------------------------------------------------
	// INSUFFICIENT STOCK
	// --------------------------------------------------------

	order, err = createOrder(1, 100, 10)

	if err != nil {
		handleError(err)
		return
	}

	fmt.Println("Order created successfully:")
	fmt.Println(order)
}

// ============================================================
// 9. CENTRALIZED ERROR HANDLER
// ============================================================

// handleError farklı error türlerini ayırt ediyor.
//
// Burada:
//
// errors.Is  -> belirli bir error value arıyoruz.
// errors.As  -> belirli bir error type arıyoruz.
func handleError(err error) {

	fmt.Println("ERROR:", err)

	// --------------------------------------------------------
	// errors.Is
	// --------------------------------------------------------

	if errors.Is(err, ErrUserNotFound) {
		fmt.Println("Reason: the requested user does not exist")
		return
	}

	if errors.Is(err, ErrProductNotFound) {
		fmt.Println("Reason: the requested product does not exist")
		return
	}

	if errors.Is(err, ErrInsufficientStock) {
		fmt.Println("Reason: there is not enough stock")
		return
	}

	// --------------------------------------------------------
	// errors.As
	// --------------------------------------------------------

	var validationErr *ValidationError

	if errors.As(err, &validationErr) {
		fmt.Println("Validation error detected")
		fmt.Println("Field:", validationErr.Field)
		fmt.Println("Value:", validationErr.Value)
		return
	}

	// --------------------------------------------------------
	// UNKNOWN ERROR
	// --------------------------------------------------------

	fmt.Println("Reason: unknown error")
}
