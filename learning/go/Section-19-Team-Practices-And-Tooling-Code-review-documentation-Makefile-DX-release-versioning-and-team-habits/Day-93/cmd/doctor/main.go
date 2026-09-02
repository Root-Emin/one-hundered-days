// Command doctor reports whether this machine can build and run the project.
//
//	make doctor
//
// It is the first thing scripts/setup.sh runs, and the first thing to try when
// something does not work.
package main

import (
	"context"
	"fmt"
	"os"

	_ "modernc.org/sqlite"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-93/internal/doctor"
)

func main() {
	report := doctor.Run(context.Background(), doctor.DefaultOptions())

	fmt.Println("environment check:")

	for _, result := range report.Results {
		fmt.Println(result)
	}

	ok, warn, fail := report.Counts()

	fmt.Printf("\n%d ok, %d warning(s), %d failure(s)\n", ok, warn, fail)

	if report.Failed() {
		fmt.Println("\nfix the failures above; warnings are safe to ignore for now")
		os.Exit(1)
	}

	if warn > 0 {
		fmt.Println("\nwarnings will not stop you building or testing")
	}
}
