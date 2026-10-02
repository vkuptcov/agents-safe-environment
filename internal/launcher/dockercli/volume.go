package dockercli

import (
	"context"
	"fmt"
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
