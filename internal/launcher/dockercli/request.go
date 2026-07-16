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
type CreateRequest struct {
	Image       string
	Name        string
	Runtime     string
	WorkingDir  string
	Labels      []KeyValue
	Environment []KeyValue
	Mounts      []Mount
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
