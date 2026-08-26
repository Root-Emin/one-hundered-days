package main

import (
	"fmt"
)

// --- TASK 1: Define Small Interfaces ---
// Interface 

type PaymentMethod interface {
	Pay(amount float64) string 
}

// --- TASK 2: Implement Implicitly ---

type CreditCard struct {
	CardNumber string
}

type BankTransfer struct {
	IBAN string 
}

func (c *CreditCard) Pay(amount float64) string {
	return fmt.Sprintf("Paid %.2f using credit card %s", amount, c.CardNumber)
}

func (b *BankTransfer) Pay(amount float64) string {
	return fmt.Sprintf("Paid %.2f using bank transfer %s", amount, b.IBAN)
}


// --- TASK 3: Use Interface Values ---
func CompleteOrder(method PaymentMethod, amount float64) {
	result := method.Pay(amount)
	fmt.Println("Order Succesful ", result)
}

func main() {
	card := &CreditCard{CardNumber: "1234567812345678"}
	transfer :=  &BankTransfer{IBAN: "TR123123123123123123"}

	fmt.Println("--- Normal Payment Processes (Task 3 in action) ---")
	// Passing different concrete types through the same interface-shaped API
	CompleteOrder(card, 450.0)
	CompleteOrder(transfer, 1200.0)


	fmt.Println("\n--- TASK 4: Handle nil Interfaces ---")

	var emptyCard *CreditCard = nil

	var selectedMethod PaymentMethod = emptyCard

	if selectedMethod == nil {
		fmt.Println("Error: Payment method not selected or not found")
	}else {
		fmt.Printf("System Tricked! Interface is not nil. Type: %T\n", selectedMethod)
	}

}