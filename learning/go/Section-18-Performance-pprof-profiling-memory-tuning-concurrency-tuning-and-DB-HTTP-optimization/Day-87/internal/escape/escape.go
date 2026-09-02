// Package escape shows why an allocation happens at all - and why the usual
// folklore about it is only half true.
//
// Go decides between the stack and the heap with escape analysis: if the
// compiler can prove a value does not outlive its function, it lives on the
// stack and costs nothing to reclaim. Otherwise it "escapes to the heap" and
// becomes the garbage collector's problem.
//
// Read the compiler's reasoning with:
//
//	go build -gcflags='-m' ./internal/escape
//
// Then measure it, because the two do not always agree. Every claim in this
// file is checked by escape_test.go with testing.AllocsPerRun, and three of
// them are more interesting than the rule of thumb suggests:
//
//   - "returning a pointer allocates" - not if the function is inlined and the
//     caller does not retain the result. Escape analysis runs AFTER inlining,
//     per call site. The -m output at the definition is not the final word.
//   - "make with a runtime size heap-allocates" - not for small sizes. The
//     compiler emits a size check and uses the stack when it fits; only a
//     genuinely large slice goes to the heap.
//   - "fmt.Sprintf allocates more than strconv" - both allocate the result
//     string. fmt's extra cost is mostly CPU (format parsing, reflection),
//     which is why the benchmark measures ns/op as well as allocs/op.
package escape

import (
	"fmt"
	"strconv"
)

type Point struct {
	X, Y int
}

// Sink defeats the optimizer. Assigning to a package-level variable is a use
// the compiler cannot see through, so the value genuinely escapes - which is
// how a test can distinguish "allocated" from "optimised away".
var Sink any

// 1. RETURNING A POINTER
//
// -m reports "&Point{...} escapes to heap" here. Measured, calling this and
// discarding the result allocates NOTHING: the call inlines, and the caller's
// use does not let the value escape. Store the result in Sink and it allocates.
func NewPointEscapes(x, y int) *Point {
	return &Point{X: x, Y: y}
}

// The value version is copied into the caller's frame. For a struct this
// small, copying beats the allocation it avoids - and it can never surprise
// you by escaping through a caller.
func NewPointStack(x, y int) Point {
	return Point{X: x, Y: y}
}

// 2. THE INTERFACE BOX
//
// Passing a concrete value to a function taking `any` boxes it. The box itself
// often stays on the stack when it does not escape, so the allocation counts
// come out equal - but fmt still parses the format string and reflects over
// the argument at run time, which strconv does not.
func FormatWithFmt(value int) string {
	return fmt.Sprintf("%d", value)
}

func FormatWithStrconv(value int) string {
	return strconv.Itoa(value)
}

// 3. SLICE SIZE
//
// A constant size the compiler can see stays in the frame.
func SumStackSlice() int {
	buffer := make([]int, 64)

	for i := range buffer {
		buffer[i] = i
	}

	return sum(buffer)
}

// A runtime size is the interesting case. The compiler cannot size the frame
// for an arbitrary n, so it emits both paths: a stack buffer when the size is
// small enough, a heap allocation when it is not. Measured, size=64 allocates
// zero times and size=1<<20 allocates once.
func SumHeapSlice(size int) int {
	buffer := make([]int, size)

	for i := range buffer {
		buffer[i] = i
	}

	return sum(buffer)
}

// 4. THE CLOSURE THAT CAPTURES
//
// A closure that captures a variable and outlives the function forces that
// variable onto the heap. As with the pointer case, "outlives" is decided at
// the call site: discard the returned closure immediately and the compiler can
// still keep everything on the stack.
func SumWithEscapingClosure(values []int) func() int {
	total := sum(values)

	return func() int { return total }
}

func SumDirect(values []int) int {
	return sum(values)
}

func sum(values []int) int {
	total := 0

	for _, value := range values {
		total += value
	}

	return total
}
