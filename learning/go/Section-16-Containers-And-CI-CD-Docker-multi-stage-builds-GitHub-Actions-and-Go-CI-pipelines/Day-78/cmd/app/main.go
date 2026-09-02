// Command app is what the workflow builds.
package main

import (
	"fmt"
	"os"
	"time"

	greeting "example.com/onehundredday/Section-16-Containers-And-CI-CD-Docker-multi-stage-builds-GitHub-Actions-and-Go-CI-pipelines/Day-78/internal/greeting"
)

/*
Day 78 - Containers & CI/CD: GitHub Actions Basics

Tasks covered:

 1. .github/workflows/ci.yml, triggered on push and pull request
 2. actions/setup-go reading the version from go.mod, so CI and production
    never drift
 3. Module and build caching, so a run takes seconds rather than minutes
 4. go test ./... on every push

Files:

	.github/workflows/ci.yml       the workflow
	.github/workflows/pr-title.yml a second, tiny workflow: one job, one rule
	main.go (in the day root)      a validator that parses and checks them

Run:

	go run ./cmd/app
	go run .            # validate the workflows without pushing anything
*/

func main() {
	name := "world"

	if len(os.Args) > 1 {
		name = os.Args[1]
	}

	fmt.Println(greeting.Greet(name, time.Now()))
}
