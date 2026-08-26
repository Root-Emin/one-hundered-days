package main 

import (
	"fmt"
)

// --- DOMAIN ENTITIES (Görev 4'ün Temeli) ---

type User struct {
	Name string
	Email string
}


// --- TASK 1: Embed Structs ---

type Admin struct {
	User
	Level int
}

// --- BEHAVIORS (Davranışlar) ---

// Normal User davranışı

func (u *User) Authenticate() {
	fmt.Printf("User Authenticated: %s (%s)\n", u.Name, u.Email)
}

// Normal User davranışı (Ezilecek olan)

func (u *User) ShowProfile() {
	fmt.Printf("Standard Profile -> Name: %s\n", u.Name)
}

// --- TASK 3: Override Methods (Shadowing) ---
// Admin'e özel ShowProfile metodu yazarak User'ınkini eziyoruz.

func (a *Admin) ShowProfile() {
	fmt.Printf("ADMIN Profile -> Name %s | Level %d\n", a.Name, a.Level)
}


func main(){
	regularUser := &User{
		Name: "Emin Kutlu",
		Email: "emin@test.com",
	}

	superAdmin := &Admin{
		User: User{
			Name:  "System Boss",
			Email: "admin@test.com",
		},
		Level: 5,
	}

	fmt.Println("--- Regular User İşlemleri ---")
	regularUser.Authenticate()
	regularUser.ShowProfile()

	fmt.Println("\n--- Admin İşlemleri ---")
	// --- TASK 2: Compose Behavior ---
	// Adminin kendi Authenticate metodu yok. 
	// Ama Userı gömdüğümüz için onu kullanabiliyor (Promotion)
	superAdmin.Authenticate() 
	
	// TASK 3 Kanıtı: Burada User'ın değil, Admin'in ezdiği ShowProfile çalışır.
	superAdmin.ShowProfile()

}