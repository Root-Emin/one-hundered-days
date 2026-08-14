package main

import "fmt"

func Hello(name string) string {
	message := fmt.Sprintf("Hi %v. Welcome!", name)
	return message
}

func main() {
	fmt.Println(Hello("Emin"))
	var age int = 19
	height := 1.81
	var isStudent bool = true
	fmt.Println("My age is", age)
	fmt.Println("My height is", height)
	fmt.Println("Am I a student?", isStudent)
	fmt.Println(Hello("Emin"), "My age is", age, "My height is", height, "Am I a student?", isStudent)

}
