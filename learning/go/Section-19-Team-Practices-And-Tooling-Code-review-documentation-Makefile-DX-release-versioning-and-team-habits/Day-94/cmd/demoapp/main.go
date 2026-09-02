// Command demoapp is the artifact the reproducible-build check compiles.
//
// It is small on purpose: the point is the build flags, not the program.
package main

import (
	"flag"
	"fmt"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-94/internal/buildinfo"
)

func main() {
	version := flag.Bool("version", false, "print the build information and exit")

	flag.Parse()

	if *version {
		fmt.Println(buildinfo.String())

		return
	}

	fmt.Println("demoapp", buildinfo.Version)
}
