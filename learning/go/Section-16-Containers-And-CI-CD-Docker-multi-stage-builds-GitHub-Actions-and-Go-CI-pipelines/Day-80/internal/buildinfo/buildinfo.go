// Package buildinfo carries the identity of the running binary.
//
// "Which version is in production?" must have an answer that does not depend
// on somebody remembering what they deployed.
package buildinfo

import (
	"runtime"
	"runtime/debug"
	"time"
)

// Set at link time:
//
//	-ldflags "-X .../internal/buildinfo.Version=v1.0.0 -X .../buildinfo.Commit=abc123"
var (
	Version   = "dev"
	Commit    = "none"
	BuildTime = "unknown"
)

type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`
	StartedAt string `json:"started_at"`
	Uptime    string `json:"uptime"`
}

var startedAt = time.Now()

func Current() Info {
	commit := Commit

	// When -X is not supplied, fall back to what the toolchain recorded from
	// git. A binary built with `go build` inside a checkout still knows its
	// revision.
	if commit == "none" {
		if build, ok := debug.ReadBuildInfo(); ok {
			for _, setting := range build.Settings {
				if setting.Key == "vcs.revision" && setting.Value != "" {
					commit = setting.Value[:min(7, len(setting.Value))]
				}
			}
		}
	}

	return Info{
		Version:   Version,
		Commit:    commit,
		BuildTime: BuildTime,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
		StartedAt: startedAt.UTC().Format(time.RFC3339),
		Uptime:    time.Since(startedAt).Round(time.Second).String(),
	}
}
