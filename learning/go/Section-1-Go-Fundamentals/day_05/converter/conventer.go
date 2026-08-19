// Package converter sıcaklık birimleri arasında dönüşüm yapar.
package converter

func CelsiusToFahrenheit(c float64) float64 {
	return (c * 9 / 5) + 32
}

func FahrenheitToCelsius(f float64) float64 {
	return (f - 32) * 5 / 9
}
