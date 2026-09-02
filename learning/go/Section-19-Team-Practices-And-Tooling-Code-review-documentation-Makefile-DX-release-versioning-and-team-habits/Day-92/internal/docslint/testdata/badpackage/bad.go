package badpackage

// This package deliberately breaks every rule docslint checks, so the tests
// and the demo have something real to report. It lives under testdata, which
// the go tool ignores, so it is never built or vetted with the rest of the
// module.

// Returns the widget count.
func CountWidgets() int {
	return 0
}

func UndocumentedFunction() {}

type UndocumentedType struct{}

// A widget.
type Widget struct {
	Name string
}

const MaxWidgets = 10

// The default timeout.
var DefaultTimeout = 30

func unexportedIsFine() {}

type unexportedTypeIsFine struct{}
