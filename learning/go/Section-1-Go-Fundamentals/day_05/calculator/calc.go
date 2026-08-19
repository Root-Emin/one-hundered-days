// Package calculator temel dört işlem fonksiyonlarını sunar.
package calculator

import (
	"errors"
)

func Calculate(num1 float64, operator string, num2 float64) (float64, error) {
	switch operator {
	case "+":
		return num1 + num2, nil

	case "-":
		return num1 - num2, nil

	case "*":
		return num1 * num2, nil

	case "/":
		// EDGE CASE : Division by zero
		if num2 == 0 {
			return 0, errors.New("0 ile bölme hatası")
		}
		return num1 / num2, nil

	default:
		return 0, errors.New("geçersiz işlem")
	}
}
