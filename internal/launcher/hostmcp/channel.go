package hostmcp

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/vkuptcov/agents-safe-environment/internal/mcpchannel"
)

const (
	// runtimeDirEnv names the user runtime directory the channel lives in. It must be set and owned
	// by the invoking user: falling back to a world-traversable temporary directory would quietly
	// drop the access control this design relies on.
	runtimeDirEnv = "XDG_RUNTIME_DIR"

	// channelRoot is the project-independent root below the runtime directory.
	channelRoot = "agents-safe"

	// SessionTarget is the session container's mount target: the generation directory only.
	//
	// It is deliberately not under /run/agents-safe/, which `serve` creates and chowns for the session
	// manager; a bind mount inside that directory would entangle two lifetimes for no reason.
	SessionTarget = "/run/agents-safe-host-mcp"

	// SidecarTarget is the relay sidecar's mount target: the project runtime parent, not just its own
	// generation, because it removes that directory after established-lease EOF.
	SidecarTarget = "/run/agents-safe-mcp"

	// generationBytes sizes the random generation identifier. Sixteen hex characters keep the deepest
	// host socket path near 80 bytes, inside the sockaddr_un limit, while staying unguessable enough
	// that holding a sidecar's name requires already knowing it.
	generationBytes = 8

	// socketPathLimit is the platform's sockaddr_un limit. The launcher validates it during preflight
	// because the failure is otherwise a confusing bind error at container start.
	socketPathLimit = 108

	// directoryMode keeps the channel private to the invoking user.
	directoryMode = 0o700
)

// Channel is one session generation's directory on the host.
type Channel struct {
	// Parent is <runtime-dir>/agents-safe/<project key>/, the sidecar's mount.
	Parent string
	// Generation is <parent>/<generation>/, the session container's mount.
	Generation string
	// Name is the generation identifier alone, which the sidecar's container name embeds.
	Name string
}

// NewChannel allocates a fresh generation directory for one launch attempt.
//
// Every session creation gets a new random generation, and a directory is never reused by a later
// session. That is what makes cleanup safe: successive containers for one worktree share a project
// key and a container name, so a directory keyed only by project would let a departing sidecar
// delete the channel of the session that replaced it.
func NewChannel(lookupEnv func(string) (string, bool), projectKey string, endpoints int) (Channel, error) {
	runtimeDir, err := validatedRuntimeDir(lookupEnv)
	if err != nil {
		return Channel{}, err
	}
	name, err := generationName()
	if err != nil {
		return Channel{}, err
	}
	channel := Channel{
		Parent:     filepath.Join(runtimeDir, channelRoot, projectKey),
		Generation: filepath.Join(runtimeDir, channelRoot, projectKey, name),
		Name:       name,
	}
	if err := channel.validateSocketPaths(endpoints); err != nil {
		return Channel{}, err
	}
	if err := channel.materialize(false); err != nil {
		return Channel{}, err
	}
	return channel, nil
}

// AdoptChannel reconstructs a channel from the generation directory a running session recorded.
//
// The recorded path is read, never compared: a reusing launcher has already allocated its own
// candidate, which necessarily differs, and it adopts this one instead.
func AdoptChannel(generation string) (Channel, error) {
	if !filepath.IsAbs(generation) {
		return Channel{}, fmt.Errorf("recorded host MCP channel %q is not absolute", generation)
	}
	name := filepath.Base(generation)
	if name == "." || name == string(filepath.Separator) {
		return Channel{}, fmt.Errorf("recorded host MCP channel %q names no generation", generation)
	}
	return Channel{
		Parent:     filepath.Dir(generation),
		Generation: generation,
		Name:       name,
	}, nil
}

// EnsureChannel recreates the generation directory a stopped persistent session recorded, so the
// session's bind mount has a source again before it is started.
//
// The departed sidecar removed that directory after lease EOF, and a logout may have cleared the
// runtime directory entirely, so the path is recreated rather than assumed. The recorded path is
// trusted only within this project's own runtime parent: anything else fails closed before any
// directory is created, so a mislabelled container cannot make the launcher create directories
// elsewhere.
func EnsureChannel(lookupEnv func(string) (string, bool), projectKey string, generation string) (Channel, error) {
	runtimeDir, err := validatedRuntimeDir(lookupEnv)
	if err != nil {
		return Channel{}, err
	}
	channel, err := AdoptChannel(generation)
	if err != nil {
		return Channel{}, err
	}
	if want := filepath.Join(runtimeDir, channelRoot, projectKey); channel.Parent != want {
		return Channel{}, fmt.Errorf(
			"recorded host MCP channel %q is outside this project's runtime parent %q", generation, want)
	}
	if err := channel.materialize(true); err != nil {
		return Channel{}, err
	}
	return channel, nil
}

// materialize creates the channel's directories with the private mode. An existing generation is an
// error unless existingOK, which the restart path passes because a stopped session's directory may
// still be present.
//
// MkdirAll and Mkdir both honour the umask, so the mode this channel relies on is applied rather
// than assumed. If that fails on a directory this call created, it is removed before returning: the
// caller never receives a Channel for a failed allocation, so its own candidate cleanup could not
// find this one.
func (channel Channel) materialize(existingOK bool) error {
	if err := os.MkdirAll(channel.Parent, directoryMode); err != nil {
		return fmt.Errorf("create host MCP runtime parent %q: %w", channel.Parent, err)
	}
	created := true
	if err := os.Mkdir(channel.Generation, directoryMode); err != nil {
		if !existingOK || !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("create host MCP generation %q: %w", channel.Generation, err)
		}
		created = false
	}
	if err := os.Chmod(channel.Generation, directoryMode); err != nil {
		if created {
			_ = os.RemoveAll(channel.Generation)
		}
		return fmt.Errorf("set host MCP generation mode %q: %w", channel.Generation, err)
	}
	return nil
}

// Remove deletes a candidate generation this attempt allocated.
//
// A launcher removes only what its own attempt created. It never removes a generation merely because
// no running session label references it: that test is racy before session-container creation, and
// crash residue is inert inside the user's own runtime directory.
func (channel Channel) Remove() error {
	if channel.Generation == "" {
		return nil
	}
	return os.RemoveAll(channel.Generation)
}

// ControlPath is the readiness and lease socket, on the host side of the mount.
func (channel Channel) ControlPath() string {
	return filepath.Join(channel.Generation, mcpchannel.ControlSocketName)
}

// SidecarGeneration is this generation as the sidecar sees it through its runtime-parent mount.
func (channel Channel) SidecarGeneration() string {
	return filepath.Join(SidecarTarget, channel.Name)
}

// validatedRuntimeDir requires a runtime directory that exists and belongs to the invoking user.
func validatedRuntimeDir(lookupEnv func(string) (string, bool)) (string, error) {
	runtimeDir, found := lookupEnv(runtimeDirEnv)
	if !found || runtimeDir == "" {
		return "", fmt.Errorf(
			"%s is not set, so there is no private directory to place the host MCP channel in; "+
				"set it or relaunch with --no-host-mcp", runtimeDirEnv)
	}
	if !filepath.IsAbs(runtimeDir) {
		return "", fmt.Errorf("%s %q is not absolute", runtimeDirEnv, runtimeDir)
	}
	info, err := os.Stat(runtimeDir)
	if err != nil {
		return "", fmt.Errorf("inspect %s %q: %w", runtimeDirEnv, runtimeDir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s %q is not a directory", runtimeDirEnv, runtimeDir)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("cannot read ownership of %s %q", runtimeDirEnv, runtimeDir)
	}
	if int(stat.Uid) != os.Getuid() {
		return "", fmt.Errorf(
			"%s %q is owned by uid %d, not by the invoking user %d; the host MCP channel must be private",
			runtimeDirEnv, runtimeDir, stat.Uid, os.Getuid())
	}
	return runtimeDir, nil
}

func generationName() (string, error) {
	buffer := make([]byte, generationBytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("allocate host MCP generation: %w", err)
	}
	return "g-" + hex.EncodeToString(buffer), nil
}

// validateSocketPaths rejects a channel whose sockets would not fit sockaddr_un, on either side of
// the mount, before any container is created.
func (channel Channel) validateSocketPaths(endpoints int) error {
	candidates := []string{
		channel.ControlPath(),
		filepath.Join(channel.SidecarGeneration(), mcpchannel.ControlSocketName),
	}
	for index := range endpoints {
		candidates = append(candidates,
			filepath.Join(channel.Generation, mcpchannel.SocketName(index)),
			filepath.Join(SessionTarget, mcpchannel.SocketName(index)),
			filepath.Join(channel.SidecarGeneration(), mcpchannel.SocketName(index)),
		)
	}
	for _, path := range candidates {
		if len(path) >= socketPathLimit {
			return fmt.Errorf(
				"host MCP socket path %q is %d bytes, over the %d-byte platform limit; "+
					"use a shorter %s or relaunch with --no-host-mcp",
				path, len(path), socketPathLimit, runtimeDirEnv)
		}
	}
	return nil
}

// Environment encodes AGENTS_SAFE_HOST_MCP: the endpoint set as the session container sees it.
func (set Set) Environment() (string, error) {
	wire := make([]mcpchannel.Endpoint, 0, len(set.Endpoints))
	for index, endpoint := range set.Endpoints {
		wire = append(wire, mcpchannel.Endpoint{
			Listen: endpoint.Listen(),
			Socket: filepath.Join(SessionTarget, mcpchannel.SocketName(index)),
		})
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return "", fmt.Errorf("encode host MCP endpoint set: %w", err)
	}
	return string(encoded), nil
}

// RelayCommand is the sidecar's Docker command: the mode, its generation as the sidecar sees it, and
// the endpoints in the set's sorted order, which fixes each endpoint's socket index.
func (set Set) RelayCommand(channel Channel, initialLeaseTimeout time.Duration) []string {
	command := []string{
		"relay",
		"--generation", channel.SidecarGeneration(),
		"--initial-lease-timeout", initialLeaseTimeout.String(),
	}
	for _, endpoint := range set.Endpoints {
		command = append(command, "--endpoint", endpoint.Address())
	}
	return command
}
