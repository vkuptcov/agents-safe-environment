package dockercli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// DaemonInspection is the operating system and architecture the connected Docker daemon executes
// containers for. Both fields already use Go's GOOS/GOARCH naming: `docker version` reports the
// daemon's own build target that way, so a caller can compare it with a container target without
// substituting the CLI host's runtime.GOOS/GOARCH.
type DaemonInspection struct {
	OS           string
	Architecture string
}

// InspectDaemon returns the connected Docker daemon's operating system and architecture. It never
// reads the host runtime.GOOS/GOARCH: the daemon may run remotely, or on a different platform than the
// CLI host, and a session backend that assumed otherwise would select the wrong container target.
//
// A non-Linux operating system or an unsupported architecture is a valid negative, not an error here:
// InspectDaemon reports exactly what the daemon claims, and codexinstall.ParseTarget is the layer that
// rejects it. Only a transport failure or an unparseable response is returned as an error.
func (client *Client) InspectDaemon(ctx context.Context) (DaemonInspection, error) {
	output, err := client.combinedOutput(ctx, "version", "--format", "{{json .Server}}")
	if err != nil {
		return DaemonInspection{}, commandFailure("inspect Docker daemon", output, err)
	}
	var server struct {
		Os   string `json:"Os"`
		Arch string `json:"Arch"`
	}
	if err := json.Unmarshal(output, &server); err != nil {
		return DaemonInspection{}, fmt.Errorf("parse Docker daemon version: %w", err)
	}
	if strings.TrimSpace(server.Os) == "" || strings.TrimSpace(server.Arch) == "" {
		return DaemonInspection{}, fmt.Errorf(
			"Docker daemon version response is missing OS/architecture: %s", strings.TrimSpace(string(output)),
		)
	}
	return DaemonInspection{OS: server.Os, Architecture: server.Arch}, nil
}
