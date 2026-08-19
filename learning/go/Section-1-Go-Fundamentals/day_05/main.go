package main

import (
	"fmt"
	"strings"

	"example.com/onehundredday/Section-1-Go-Fundamentals/day_05/calculator"
	"example.com/onehundredday/Section-1-Go-Fundamentals/day_05/converter"
)

func main() {
	// --- GÖREV 1: HESAP MAKİNESİ (CLI) ---
	var n1, n2 float64
	var operator string

	fmt.Println("=== Go CLI Hesap Makinesi ===")
	fmt.Print("İşlemi girin (Örn: 5 + 3 veya 10.5 / 2): ")

	_, err := fmt.Scanln(&n1, &operator, &n2)

	if err != nil {
		fmt.Println("Girdi okunamadı! Lütfen aralarında boşluk bırakarak geçerli bir işlem girin. (örn: 8 + 2)")
		return
	}

	result, calcErr := calculator.Calculate(n1, operator, n2)

	if calcErr != nil {
		fmt.Println("Hata:", calcErr)
		return
	}
	fmt.Printf("Sonuç: %v %s %v = %v\n", n1, operator, n2, result)

	// --- GÖREV 2: SICAKLIK DÖNÜŞTÜRÜCÜ ---
	var temp float64
	var unit string

	fmt.Println("=== Sıcaklık Dönüştürücü ===")
	fmt.Print("Değer ve birim girin (Örn: 25 C veya 77 F): ")

	_, errTemp := fmt.Scanln(&temp, &unit)
	if errTemp != nil {
		fmt.Println("Lütfen geçerli bir format girin (Örn: 25 C)")
		return
	}

	unit = strings.ToUpper(unit)

	if unit == "C" {
		f := converter.CelsiusToFahrenheit(temp)
		fmt.Printf("%.2f°C = %.2f°F\n", temp, f)
	} else if unit == "F" {
		c := converter.FahrenheitToCelsius(temp)
		fmt.Printf("%.2f°F = %.2f°C\n", temp, c)
	} else {
		fmt.Println("Hata: Sadece 'C' (Celsius) veya 'F' (Fahrenheit) desteklenir.")
	}
}
