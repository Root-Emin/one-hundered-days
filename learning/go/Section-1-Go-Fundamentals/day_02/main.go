package main

import "fmt"

func main() {
	age := 20

	if age >= 18 {
		fmt.Println("You are an adult.")
	} else {
		fmt.Println("You are a minor.")
	}

	score := 75

	if score >= 90 {
		fmt.Println("AA")
	} else if score >= 70 {
		fmt.Println("BB")
	} else if score >= 50 {
		fmt.Println("CC")
	} else {
		fmt.Println("Kaldın")
	}

	for i := 1; i <= 5; i++ {
		fmt.Println(i)
	}

	names := []string{"Alice", "Bob", "Charlie"}

	for index, name := range names {
		fmt.Println(index, name)
	}

	role := "admin"

	switch role {
	case "admin":
		fmt.Println("You have full access.")

	case "staff":
		fmt.Println("You have permission to personel console")

	case "customer":
		fmt.Println("You have permission to customer console")

	default:
		fmt.Println("You have no access.")
	}

	for i := 1; i <= 10; i++ {
		if i == 5 {
			continue
		}
		if i == 8 {
			break
		}
		fmt.Println(i)
	}
}
