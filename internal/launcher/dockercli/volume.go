package dockercli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// VolumeInspection is the subset of `docker volume inspect` state used to validate ownership of the
// Codex installation store.
type VolumeInspection struct {
	Name   string            `json:"Name"`
	Labels map[string]string `json:"Labels"`
}

// InspectVolume returns one named volume's transport state. Found is false only when Docker reports no
// such volume; any other failure is a transport error, and the caller must not treat it as absence.
func (client *Client) InspectVolume(ctx context.Context, name string) (VolumeInspection, bool, error) {
	if strings.TrimSpace(name) == "" {
		return VolumeInspection{}, false, errors.New("volume name is required")
	}
	output, err := client.combinedOutput(ctx, "volume", "inspect", name)
	if err != nil {
		if isVolumeNotFound(output, err) {
			return VolumeInspection{}, false, nil
		}
		return VolumeInspection{}, false, commandFailure(fmt.Sprintf("inspect volume %q", name), output, err)
	}
	var inspections []VolumeInspection
	if err := json.Unmarshal(output, &inspections); err != nil {
		return VolumeInspection{}, false, fmt.Errorf("parse volume inspection: %w", err)
	}
	if len(inspections) != 1 || inspections[0].Name != name {
		return VolumeInspection{}, false, fmt.Errorf(
			"inspect volume %q returned %d records", name, len(inspections),
		)
	}
	return inspections[0], true, nil
}

// CreateVolume creates one named local volume with the given labels. Callers should reach it only
// after InspectVolume reported the name absent. Note that `docker volume create` is itself
// idempotent: run against a name that already exists it is a no-op success and does NOT re-apply the
// requested labels. Absence-before-create is therefore not sufficient to guarantee ownership, because
// another party can win the race in between; a caller that needs a fail-closed ownership guarantee
// must re-inspect the resolved volume and validate its labels after this returns.
func (client *Client) CreateVolume(ctx context.Context, name string, labels []KeyValue) error {
	arguments, err := BuildVolumeCreateArgs(name, labels)
	if err != nil {
		return err
	}
	output, err := client.combinedOutput(ctx, arguments...)
	if err != nil {
		return commandFailure(fmt.Sprintf("create volume %q", name), output, err)
	}
	return nil
}

// BuildVolumeCreateArgs encodes a typed volume-create request as Docker CLI argv.
func BuildVolumeCreateArgs(name string, labels []KeyValue) ([]string, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("volume name is required")
	}
	args := []string{"volume", "create"}
	for _, label := range labels {
		args = append(args, "--label", label.Key+"="+label.Value)
	}
	return append(args, name), nil
}

func isVolumeNotFound(output []byte, err error) bool {
	if ExitCode(err) != 1 {
		return false
	}
	message := strings.ToLower(string(output))
	return strings.Contains(message, "no such volume")
}
