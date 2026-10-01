package dockercli

import (
	"context"
	"fmt"
	"sort"
)

// EnsureVolume creates persistent storage before container creation. Docker preserves an existing
// volume and its labels; concurrent creation of the same name is handled by the daemon.
func (client *Client) EnsureVolume(ctx context.Context, name string, labels map[string]string) error {
	args := []string{"volume", "create"}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "--label", key+"="+labels[key])
	}
	args = append(args, name)
	output, err := client.combinedOutput(ctx, args...)
	if err != nil {
		return commandFailure(fmt.Sprintf("create nested Docker storage volume %q", name), output, err)
	}
	return nil
}
