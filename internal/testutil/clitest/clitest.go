// Package clitest provides shared launcher-CLI test doubles used by the cli package tests and the
// command mains' tests, so the fake launcher and its recorded fields live in one place.
package clitest

import (
	"context"

	"github.com/vkuptcov/agents-safe-environment/internal/cli"
	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
)

// RecordingLauncher records the arguments of the last Launch call and returns a fixed error.
type RecordingLauncher struct {
	LaunchPlan    launchplan.Plan
	Image         string
	Command       []string
	Options       launchplan.Options
	Err           error
	PanicOnLaunch bool
}

// Launch records its arguments, or panics when PanicOnLaunch is set for tests that must return
// before any launch.
func (recording *RecordingLauncher) Launch(
	_ context.Context,
	plan launchplan.Plan,
	image string,
	command []string,
	options launchplan.Options,
) error {
	if recording.PanicOnLaunch {
		panic("Launch should not be called")
	}
	recording.LaunchPlan = plan
	recording.Image = image
	recording.Command = append([]string(nil), command...)
	recording.Options = options
	return recording.Err
}

// PanicDependencies returns dependencies whose operations panic, for tests that must return before
// reaching discovery, launch-plan construction, or container launch.
func PanicDependencies() cli.Dependencies {
	return cli.Dependencies{
		Discover: func(context.Context, string) (gitproject.Project, error) { panic("discover should not be called") },
		BuildLaunchPlan: func(gitproject.Project) (launchplan.Plan, error) {
			panic("BuildLaunchPlan should not be called")
		},
		Launcher: &RecordingLauncher{PanicOnLaunch: true},
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
