package container

import "github.com/vkuptcov/agents-safe-environment/internal/session"

// containerPaths centralizes absolute paths inside the Sysbox container: image-owned inputs and
// its writable runtime filesystem, never host paths. The one exception is dockerdCommand, which
// production resolves through PATH. Tests can redirect privileged filesystem operations without
// changing the public launcher contract.
type containerPaths struct {
	bashRCSource       string
	sudoersFile        string
	dockerRunDirectory string
	dockerDataRoot     string
	dockerSocket       string
	dockerdLog         string
	crunBinary         string
	sessionSocket      string
	dockerdCommand     string
}

func defaultContainerPaths() containerPaths {
	return containerPaths{
		bashRCSource:       "/etc/agents-safe/bashrc",
		sudoersFile:        "/etc/sudoers.d/agents-safe-host",
		dockerRunDirectory: "/run/docker",
		dockerDataRoot:     "/var/lib/docker",
		dockerSocket:       "/var/run/docker.sock",
		dockerdLog:         "/tmp/agents-safe-dockerd.log",
		crunBinary:         "/usr/local/bin/crun",
		sessionSocket:      session.DefaultSocketPath,
		dockerdCommand:     "dockerd",
	}
}
