package main 

import (
	"fmt"
)

type BankAccount struct {
	Owner string
	Balance float64
}

// GÖREV 1: Value Receiver 

func (account BankAccount) ShowBalance () {
	fmt.Printf("Current balance of %s: %.2f TL\n", account.Owner, account.Balance)
}


// GÖREV 2: Pointer Receiver 
func (account *BankAccount) Deposit(amount float64) {
	account.Balance += amount
	fmt.Printf("%.2f TL deposited into %s's account\n", amount, account.Owner)
}

// Kopyada kaldığı için pointer receiver kullanılır.
func (account BankAccount) FakeDeposit (amount float64) {
	account.Balance += amount
	fmt.Printf("%.2f TL deposited into %s's account\n", amount, account.Owner)
}

func main() {
	account := BankAccount{
		Owner: "John Doe",
		Balance: 1000.00,
	}

	account.ShowBalance()
	account.Deposit(500.00)
	account.ShowBalance()
	account.FakeDeposit(500.00)
	account.ShowBalance()
}

/*
---------------------------------------------------------
GÖREV 4: METHOD SETS (Metot Kümeleri) ÖZETİ
---------------------------------------------------------
Bir struct'ın hangi metotlara sahip olduğu, o struct'ı değer (value) 
olarak mı yoksa işaretçi (pointer) olarak mı kullandığımıza göre değişir:

1. BankAccount (Değer/Value) Metot Kümesi:
   - Yalnızca değer alıcısına (value receiver) sahip metotları içerir.
   - Sete Dahil Olanlar: ShowBalance, FakeDeposit

2. *BankAccount (İşaretçi/Pointer) Metot Kümesi:
   - HEM değer HEM DE işaretçi alıcılarına sahip TÜM metotları içerir.
   - Sete Dahil Olanlar: ShowBalance, FakeDeposit + Deposit

NEDEN ÖNEMLİ? (Interface Bağlantısı)
Eğer yarın bir Interface oluşturur ve içine "Deposit" metodunu zorunlu kılarsak; 
bu Interface'e sadece bir pointer (*BankAccount) atayabiliriz. Normal bir değer (BankAccount) 
bu Interface'i sağlayamaz, çünkü onun metot kümesinde Deposit yoktur!

completed=true
---------------------------------------------------------
*/