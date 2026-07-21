// Package dockercli performs typed container operations through the host Docker CLI.
package dockercli

// KeyValue is one ordered Docker label or environment entry.
type KeyValue struct {
	Key   string
	Value string
}

// MountKind selects the Docker `--mount type=...` a Mount renders as. The zero value is
// MountKindBind, so every existing bind-mount literal in the codebase keeps its exact meaning without
// being touched by this addition.
type MountKind int

const (
	// MountKindBind renders "type=bind" with rprivate propagation, matching every mount before this
	// type existed.
	MountKindBind MountKind = iota
	// MountKindVolume renders "type=volume" and never carries bind-propagation, which is a bind-only
	// concept. BuildCreateArgs requires ReadOnly for this kind, since the only volume mount an
	// ordinary session container may request is the read-only Codex installation store.
	MountKindVolume
)

// Mount is one bind or named-volume mount already selected and validated by the launcher.
type Mount struct {
	// Kind selects bind or named-volume argv rendering. The zero value is MountKindBind.
	Kind     MountKind
	Source   string
	Target   string
	ReadOnly bool
}

// CreateRequest contains the Docker-specific inputs for one detached container.
//
// It describes two roles. The session container carries an explicit Runtime and WorkingDir and no
// Command, so Docker appends the image's default. The relay sidecar carries a Command, the host
// network namespace, and every confinement below, and takes the Docker default runtime.
//
// Runtime is optional here rather than required, which the session container's own builder
// compensates for: the invariant that only the sidecar may take the Docker default is enforced where
// session containers are created, not in this shared argv builder.
type CreateRequest struct {
	Image string
	Name  string
	// Runtime is the explicit Docker runtime. Empty selects the Docker default, which only the relay
	// sidecar may do.
	Runtime string
	// WorkingDir is empty for a container that has no project directory to enter.
	WorkingDir string
	// User is the numeric uid:gid the container process runs as. Empty keeps the image's user.
	User string
	// NetworkMode is empty for the session container's own namespace, or "host" for the relay
	// sidecar, whose only reason to exist is reaching host loopback.
	NetworkMode string
	// ReadOnlyRootfs makes the container's root filesystem read-only.
	ReadOnlyRootfs bool
	// CapDrop lists capabilities to drop; "ALL" drops every one.
	CapDrop []string
	// SecurityOpt lists Docker security options, such as no-new-privileges.
	SecurityOpt []string
	Labels      []KeyValue
	Environment []KeyValue
	Mounts      []Mount
	// Command replaces the image's default command. Empty preserves it.
	Command []string
}

// ExecRequest contains the Docker-specific inputs for one command in an existing container.
type ExecRequest struct {
	ContainerID string
	User        string
	WorkingDir  string
	Environment []KeyValue
	Command     []string
	Interactive bool
	AllocateTTY bool
}

// BuildRequest contains the transport inputs for one project-image build. The launcher owns all
// policy: this type only preserves the already-selected tag, base reference, and fixed context as
// separate Docker argv values. Docker uses the validated context's default Dockerfile.
type BuildRequest struct {
	Tag       string
	BaseImage string
	Context   string
}

// ImageInspection is the image-config subset used to validate a built project image.
type ImageInspection struct {
	ID           string `json:"Id"`
	Architecture string `json:"Architecture"`
	Config       struct {
		User        string   `json:"User"`
		Entrypoint  []string `json:"Entrypoint"`
		Command     []string `json:"Cmd"`
		Environment []string `json:"Env"`
	} `json:"Config"`
}

// ContainerInspection is the subset of Docker inspect state used by launcher lifecycle policy.
type ContainerInspection struct {
	ID string `json:"Id"`
	// Image is the immutable content ID this container was created from. On reuse it is
	// authoritative: a replacement sidecar must match the already-running session rather than a
	// mutable tag that may have moved since.
	Image  string `json:"Image"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Running bool   `json:"Running"`
		Status  string `json:"Status"`
	} `json:"State"`
}
