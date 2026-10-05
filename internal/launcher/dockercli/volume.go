package dockercli

import (
	"context"
	"fmt"
	"strings"
)

// EnsureVolume creates a named volume with creation-time labels. Docker preserves an existing volume and
// its labels, so a caller may call this before every create without changing retained storage.
func (client *Client) EnsureVolume(ctx context.Context, name string, labels []KeyValue) error {
	args := append([]string{"volume", "create"}, labelArgs(labels)...)
	output, err := client.combinedOutput(ctx, append(args, name)...)
	if err != nil {
		return commandFailure(fmt.Sprintf("create volume %q", name), output, err)
	}
	return nil
}

// RunningContainersUsingVolume lists the names of running containers that mount the named volume. Docker
// matches the volume filter against the exact name, and `docker ps` without -a omits stopped containers.
func (client *Client) RunningContainersUsingVolume(ctx context.Context, name string) ([]string, error) {
	output, err := client.combinedOutput(ctx, "ps", "--filter", "volume="+name, "--format", "{{.Names}}")
	if err != nil {
		return nil, commandFailure(fmt.Sprintf("list containers using volume %q", name), output, err)
	}
	return strings.Fields(string(output)), nil
}
