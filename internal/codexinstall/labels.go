package codexinstall

import "strconv"

// Label is one ownership label key/value pair. It intentionally avoids any Docker-specific type so
// this package stays independent of the Docker transport; internal/launcher converts it to the
// transport's own key/value type before calling dockercli.
type Label struct {
	Key   string
	Value string
}

// Ownership label keys and fixed values, matching persistent-codex-installation.md §1.
const (
	LabelManaged       = "codex-safe.managed"
	LabelResource      = "codex-safe.resource"
	LabelHostUID       = "codex-safe.host-uid"
	LabelCodexTarget   = "codex-safe.codex-target"
	LabelStoreProtocol = "codex-safe.codex-store-protocol"

	LabelManagedValue         = "true"
	ResourceCodexInstallation = "codex-installation"
)

// Labels returns this identity's ownership labels in the exact order the design requires: managed,
// resource, host UID, target, then store protocol. An existing volume at the deterministic name must
// match every one of these labels before any container may mount it; the launcher never adopts,
// relabels, or deletes a volume that fails this check.
func (identity Identity) Labels() []Label {
	return []Label{
		{Key: LabelManaged, Value: LabelManagedValue},
		{Key: LabelResource, Value: ResourceCodexInstallation},
		{Key: LabelHostUID, Value: strconv.Itoa(identity.HostUID)},
		{Key: LabelCodexTarget, Value: identity.Target.String()},
		{Key: LabelStoreProtocol, Value: strconv.Itoa(identity.Protocol)},
	}
}
