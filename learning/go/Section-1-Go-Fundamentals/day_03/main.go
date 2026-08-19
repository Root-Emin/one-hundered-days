package main

import (
	"fmt"
	"log"

	"example.com/onehundredday/Section-1-Go-Fundamentals/day_03/greetings"
)

func main() {
	message, err := greetings.Hello("Emin")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(message)

	letters, words := greetings.Count("Emin Kutlu")
	fmt.Printf("%d harf, %d kelime\n", letters, words)

	if _, err := greetings.Hello("   "); err != nil {
		fmt.Println("beklenen hata:", err)
	}
}
