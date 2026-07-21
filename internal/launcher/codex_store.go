package launcher

import (
	"context"
	"fmt"

	"github.com/vkuptcov/agents-safe-environment/internal/codexinstall"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/dockercli"
)

// ResolveCodexTarget inspects the connected Docker daemon and returns the Linux container target it
// selects. It never reads the launcher host's own runtime.GOOS/GOARCH: the target always comes from
// the daemon, so a macOS launcher driving a Linux daemon still resolves the daemon's Linux target.
func ResolveCodexTarget(ctx context.Context, cli *dockercli.Client) (codexinstall.Target, error) {
	daemon, err := cli.InspectDaemon(ctx)
	if err != nil {
		return codexinstall.Target{}, fmt.Errorf("resolve Docker daemon target: %w", err)
	}
	return codexinstall.ParseTarget(daemon.OS, daemon.Architecture)
}

// EnsureCodexInstallationVolume resolves the Docker daemon's Linux target and returns the store
// identity for the given host UID, creating the deterministic installation volume when it is absent.
//
// When a volume already exists at the deterministic name, every ownership label is validated. A
// missing or conflicting label fails closed before any container can mount it: this function never
// adopts, relabels, prunes, or deletes a volume it does not already recognize as its own.
//
// Ownership is validated by name. A Docker named volume has no immutable identifier separate from its
// name, so a later session mount necessarily resolves the volume by name again. A same-name
// delete-and-recreate between this validation and that mount would defeat the check, but it requires
// host Docker access, which persistent-codex-installation.md §8 places inside the trusted computing
// base. Phase 4, which wires the read-only session mount, must validate ownership as close to mount
// creation as possible to keep that residual window minimal.
func EnsureCodexInstallationVolume(
	ctx context.Context,
	cli *dockercli.Client,
	hostUID int,
) (codexinstall.Identity, error) {
	target, err := ResolveCodexTarget(ctx, cli)
	if err != nil {
		return codexinstall.Identity{}, err
	}
	identity, err := codexinstall.NewCurrentIdentity(hostUID, target)
	if err != nil {
		return codexinstall.Identity{}, err
	}
	if err := ensureCodexVolume(ctx, cli, identity); err != nil {
		return codexinstall.Identity{}, err
	}
	return identity, nil
}

func ensureCodexVolume(ctx context.Context, cli *dockercli.Client, identity codexinstall.Identity) error {
	volumeName := identity.VolumeName()
	labels := identity.Labels()
	inspection, found, err := cli.InspectVolume(ctx, volumeName)
	if err != nil {
		return fmt.Errorf("inspect Codex installation volume %q: %w", volumeName, err)
	}
	if !found {
		if err := cli.CreateVolume(ctx, volumeName, codexLabelsToDocker(labels)); err != nil {
			return fmt.Errorf("create Codex installation volume %q: %w", volumeName, err)
		}
		// `docker volume create` is idempotent: if another party won the race between the not-found
		// inspection above and this create, Docker adopts that pre-existing volume and returns
		// success WITHOUT applying our ownership labels. Re-inspect and fall through to ownership
		// validation so a foreign volume adopted in that window is rejected rather than mounted.
		created, ok, inspectErr := cli.InspectVolume(ctx, volumeName)
		if inspectErr != nil {
			return fmt.Errorf("inspect Codex installation volume %q after create: %w", volumeName, inspectErr)
		}
		if !ok {
			return fmt.Errorf("Codex installation volume %q absent immediately after create", volumeName)
		}
		inspection = created
	}
	return validateCodexVolumeOwnership(volumeName, inspection.Labels, labels)
}

// validateCodexVolumeOwnership requires an exact match on every ownership label. Anything else
// (missing, empty, or differently valued) is treated as an unowned or foreign volume and rejected
// rather than silently reused, relabeled, or replaced.
func validateCodexVolumeOwnership(volumeName string, got map[string]string, want []codexinstall.Label) error {
	for _, label := range want {
		value, present := got[label.Key]
		if !present || value != label.Value {
			return fmt.Errorf(
				"Codex installation volume %q has label %s=%q, expected %q; refusing to use a mismatched store",
				volumeName, label.Key, value, label.Value,
			)
		}
	}
	return nil
}

func codexLabelsToDocker(labels []codexinstall.Label) []dockercli.KeyValue {
	converted := make([]dockercli.KeyValue, 0, len(labels))
	for _, label := range labels {
		converted = append(converted, dockercli.KeyValue{Key: label.Key, Value: label.Value})
	}
	return converted
}
