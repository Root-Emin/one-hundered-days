package main 

import (
	"fmt"
)

type Employee struct {
	ID int
	FirstName string
	LastName string
	Age int
	Salary int64
	FullTime bool
	Position string
	Email string
}

func (e Employee) FullName() string {
	return e.FirstName + " " + e.LastName
}

func (e Employee) String() string {
	return fmt.Sprintf("%s (%s)", e.FullName(), e.Email)
}

func main() {

	Alice := Employee{3,"Alice", "Smith", 25, 60000, true, "Manager", "alice.smith@example.com"}

	John := Employee{
		ID: 1,
		FirstName: "John",
		LastName: "Doe",
		Age: 30,
		Salary: 100000,
		FullTime: true,
		Position: "Software Engineer",
		Email: "john.doe@example.com",
	}

	Terasa := &Employee{
		ID: 2,
		FirstName: "Terasa",
		LastName: "Jones",
		Age: 28,
		Salary: 80000,
		FullTime: false,
		Position: "HR Manager",
		Email: "terasa.jones@example.com",
	}

	fmt.Println(Alice, John, Terasa)

	fmt.Println(Alice.FullName())
	fmt.Println(John.FullName())
	fmt.Println(Terasa.FullName())
}