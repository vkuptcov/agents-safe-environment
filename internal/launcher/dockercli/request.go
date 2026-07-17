// Package dockercli performs typed container operations through the host Docker CLI.
package dockercli

// KeyValue is one ordered Docker label or environment entry.
type KeyValue struct {
	Key   string
	Value string
}

// Mount is one bind mount already selected and validated by the launcher.
type Mount struct {
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

// ContainerInspection is the subset of Docker inspect state used by launcher lifecycle policy.
type ContainerInspection struct {
	ID     string `json:"Id"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Running bool   `json:"Running"`
		Status  string `json:"Status"`
	} `json:"State"`
}
