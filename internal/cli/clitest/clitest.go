// Package clitest provides shared launcher-CLI test doubles used by the cli package tests and the
// command mains' tests, so the fake launcher and its recorded fields live in one place.
package clitest

import (
	"context"

	"github.com/vkuptcov/agents-safe-environment/internal/cli"
	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher"
)

// RecordingDocker records the arguments of the last Launch call and returns a fixed error.
type RecordingDocker struct {
	Plan          launcher.Plan
	Image         string
	Command       []string
	Err           error
	PanicOnLaunch bool
}

// Launch records its arguments, or panics when PanicOnLaunch is set for tests that must return
// before any launch.
func (docker *RecordingDocker) Launch(_ context.Context, plan launcher.Plan, image string, command []string) error {
	if docker.PanicOnLaunch {
		panic("Launch should not be called")
	}
	docker.Plan = plan
	docker.Image = image
	docker.Command = append([]string(nil), command...)
	return docker.Err
}

// PanicApp returns an App whose collaborators all panic, for tests that must return before reaching
// discovery, plan building, or launch.
func PanicApp() cli.App {
	return cli.App{
		Discover:  func(context.Context, string) (gitproject.Project, error) { panic("discover should not be called") },
		BuildPlan: func(gitproject.Project) (launcher.Plan, error) { panic("buildPlan should not be called") },
		Docker:    &RecordingDocker{PanicOnLaunch: true},
	}
}

// ExitError is a Launch error that carries a process exit code, so tests can assert exit-code
// propagation.
type ExitError struct {
	Code    int
	Message string
}

func (err ExitError) Error() string { return err.Message }
func (err ExitError) ExitCode() int { return err.Code }
