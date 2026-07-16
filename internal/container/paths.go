package container

import "github.com/vkuptcov/agents-safe-environment/internal/session"

// containerPaths centralizes absolute paths inside the Sysbox container: image-owned inputs and
// its writable runtime filesystem, never host paths. Tests can redirect privileged filesystem
// operations without changing the public launcher contract.
type containerPaths struct {
	bashRCSource       string
	sudoersFile        string
	dockerRunDirectory string
	dockerDataRoot     string
	dockerSocket       string
	dockerdLog         string
	crunBinary         string
	sessionSocket      string
	dockerdBinary      string
}

func defaultContainerPaths() containerPaths {
	return containerPaths{
		bashRCSource:       "/etc/codex-safe/bashrc",
		sudoersFile:        "/etc/sudoers.d/codex-safe-host",
		dockerRunDirectory: "/run/docker",
		dockerDataRoot:     "/var/lib/docker",
		dockerSocket:       "/var/run/docker.sock",
		dockerdLog:         "/tmp/codex-safe-dockerd.log",
		crunBinary:         "/usr/local/bin/crun",
		sessionSocket:      session.DefaultSocketPath,
		dockerdBinary:      "dockerd",
	}
}
