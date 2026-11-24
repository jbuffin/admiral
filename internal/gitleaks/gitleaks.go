// Package gitleaks runs the engineer's own gitleaks on bytes Admiral is about to publish.
package gitleaks

import (
	"context"
	"os/exec"
	"time"
)

const (
	Clean  = "clean"
	Hit    = "hit"
	Failed = "failed"
	// Missing and TooOld mean no scan ran.
	Missing = "missing"
	TooOld  = "too-old"
)

type Finding struct {
	Line int    `json:"line"`
	Rule string `json:"rule"`
}

// Result is the design's scan object.
type Result struct {
	State    string    `json:"state"`
	Findings []Finding `json:"findings"`
	Error    string    `json:"error"`
	Version  string    `json:"version"`
	Install  string    `json:"install"`
}

// Runner finds and runs gitleaks. The zero value of Timeout, LookPath and Command means 30s, a PATH search of Env and exec.
type Runner struct {
	Env      []string // NAME=value, the fleet's environment, PATH included
	Timeout  time.Duration
	LookPath func(file string, env []string) (string, error)
	Command  func(ctx context.Context, name string, args ...string) *exec.Cmd
}

func (r Runner) Scan(ctx context.Context, data []byte) Result {
	return Result{State: "unimplemented"}
}
