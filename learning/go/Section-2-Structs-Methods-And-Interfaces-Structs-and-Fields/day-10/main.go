package main

import (
	"fmt"
	"math"
)

type Shape interface {
	Area() float64
	Perimeter() float64
}

type Circle struct {
	radius float64
}

type Rectangle struct {
	width  float64
	height float64
}

// 1. Dairenin Alanı (Pi * r^2)
func (c Circle) Area() float64 {
	return math.Pi * c.radius * c.radius
}

// 2. Dairenin Çevresi (2 * Pi * r)
func (c Circle) Perimeter() float64 {
	return 2 * math.Pi * c.radius
}

// 3. Dikdörtgenin Alanı (width * height)
func (r Rectangle) Area() float64 {
	return r.width * r.height
}

// 4. Dikdörtgenin Çevresi (2 * (width + height))
func (r Rectangle) Perimeter() float64 {
	return 2 * (r.width + r.height)
}

// --- GÖREV 2: Logger Soyutlaması ---
type Logger interface {
	Log(message string)
}

type ConsoleLogger struct{}

func (l ConsoleLogger) Log(message string) {
	fmt.Println("Console Log:", message)
}

// NoopLogger: Mesajı alır ama hiçbir işlem yapmaz
type NoopLogger struct{}

func (l NoopLogger) Log(message string) {
	// Hiçbir işlem yapmaz
}

// Derleyici Sigortaları (Compiler Checks)
var _ Logger = ConsoleLogger{}
var _ Logger = NoopLogger{}

type shapeTest struct {
	name              string
	shape             Shape
	expectedArea      float64
	expectedPerimeter float64
}

func main() {
	shapes := []Shape{
		Circle{radius: 5},
		Rectangle{width: 4, height: 6},
	}

	var logger Logger = ConsoleLogger{}

	// Şekilleri yazdırma
	for _, s := range shapes {
		msg := fmt.Sprintf("Area: %.2f, Perimeter: %.2f", s.Area(), s.Perimeter())
		logger.Log(msg)
	}

	// Test senaryoları
	tests := []shapeTest{
		{name: "Circle", shape: Circle{radius: 5}, expectedArea: math.Pi * 25, expectedPerimeter: 2 * math.Pi * 5},
		{name: "Rectangle", shape: Rectangle{width: 4, height: 6}, expectedArea: 24, expectedPerimeter: 20},
		{name: "Square 3x3", shape: Rectangle{width: 3, height: 3}, expectedArea: 9, expectedPerimeter: 12}, // Eklenen senaryo
	}

	// Testleri çalıştırma
	for _, test := range tests {
		area := test.shape.Area()
		perimeter := test.shape.Perimeter()

		// Kayan nokta hassasiyeti için tolerans kontrolü
		areaPass := math.Abs(area-test.expectedArea) <= 0.01
		perimPass := math.Abs(perimeter-test.expectedPerimeter) <= 0.01

		var msg string
		if areaPass && perimPass {
			msg = fmt.Sprintf("PASS: %s", test.name)
		} else {
			msg = fmt.Sprintf("FAIL: %s -> Area expected: %.2f got: %.2f | Perim expected: %.2f got: %.2f",
				test.name, test.expectedArea, area, test.expectedPerimeter, perimeter)
		}

		logger.Log(msg)
	}
}
