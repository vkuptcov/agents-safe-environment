package launcher

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
)

// codexHomeEnv is the host environment variable that overrides the default Codex home.
const codexHomeEnv = "CODEX_HOME"

// mountAbsent marks a user mount the launch resolved nothing for. It is recorded both as the mount
// source and as the mount's reuse label, so reuse can prove a running container was created with
// the same (absent) user state. The exported aliases below name it per mount for callers.
const mountAbsent = "absent"

// PersonalSkillsAbsent is recorded as the personal-skills source when the host has no
// $HOME/.agents/skills directory.
const PersonalSkillsAbsent = mountAbsent

// CodexHomeAbsent is recorded as the Codex-home source when a CodexHomeOptional launch finds no
// Codex home to mount.
const CodexHomeAbsent = mountAbsent

// CodexHomePolicy controls how a missing Codex home is handled while resolving user mounts.
type CodexHomePolicy int

const (
	// CodexHomeRequired requires a Codex home. When CODEX_HOME is unset and the default home is
	// missing, the launcher may offer to create the default; an explicitly requested CODEX_HOME
	// that is missing is always an error. This is the zero value so existing callers keep the
	// fail-closed behavior. Used by codex-safe.
	CodexHomeRequired CodexHomePolicy = iota
	// CodexHomeOptional mounts the Codex home only when it already exists. A missing home is treated
	// as absent: no Codex mount and no CODEX_HOME in the command environment. Used by agents-safe,
	// which runs arbitrary commands that do not require Codex state.
	CodexHomeOptional
)

// POSIX access(2) mode bits. The launcher supports only Linux, where these values are stable.
const (
	accessExecOK  = 0x1
	accessWriteOK = 0x2
	accessReadOK  = 0x4
)

// UserMounts holds the resolved host directories mounted as user-specific state for one container launch.
type UserMounts struct {
	// CodexHome is the canonical host directory mounted read-write as the container Codex home, or
	// CodexHomeAbsent when an optional launch resolved no Codex home.
	CodexHome string
	// PersonalSkills is the canonical host $HOME/.agents/skills directory mounted read-only, or
	// PersonalSkillsAbsent when the host has no such directory.
	PersonalSkills string
}

// mountPresent reports whether a resolved user-mount source names a directory to mount, as opposed
// to being unset or the absent marker.
func mountPresent(source string) bool {
	return source != "" && source != mountAbsent
}

// mountLabel returns the reuse label for a resolved user-mount source: the canonical source, or the
// absent marker when nothing was resolved.
func mountLabel(source string) string {
	if !mountPresent(source) {
		return mountAbsent
	}
	return source
}

// SkillsPresent reports whether a personal-skills directory was resolved for this launch.
func (mounts UserMounts) SkillsPresent() bool {
	return mountPresent(mounts.PersonalSkills)
}

// CodexHomePresent reports whether a Codex home was resolved and should be mounted for this launch.
func (mounts UserMounts) CodexHomePresent() bool {
	return mountPresent(mounts.CodexHome)
}

// codexHomeLabel returns the codex-safe.codex-home label value.
func (mounts UserMounts) codexHomeLabel() string {
	return mountLabel(mounts.CodexHome)
}

// personalSkillsLabel returns the codex-safe.personal-skills label value.
func (mounts UserMounts) personalSkillsLabel() string {
	return mountLabel(mounts.PersonalSkills)
}

// UserMountInputs carries the host inputs needed to resolve user-specific bind mounts.
type UserMountInputs struct {
	// LookupEnv reads host environment variables; it is os.LookupEnv in production.
	LookupEnv func(string) (string, bool)
	// HomeDir is the canonical absolute host home directory.
	HomeDir string
	// WritableSources are the canonical read-write host mount sources besides the Codex home
	// (the worktree root and, for a linked worktree, the common Git directory). A personal-skills
	// source overlapping any of them, or the resolved Codex home, is rejected so a read-only skill
	// cannot be modified through a writable alias.
	WritableSources []string
	// CodexHomePolicy controls how a missing Codex home is handled. The zero value is
	// CodexHomeRequired.
	CodexHomePolicy CodexHomePolicy
}

// userMountResolution separates existing mount sources from a missing implicit Codex home that may
// be created only after Docker preflight and container-reuse checks succeed.
type userMountResolution struct {
	mounts           UserMounts
	missingCodexHome string
}

// inspectUserMounts resolves existing sources without prompting or creating host state. A missing
// required implicit default is returned separately so the launcher can defer confirmation until
// Docker preflight and deterministic-container reuse checks have succeeded.
func inspectUserMounts(inputs UserMountInputs) (userMountResolution, error) {
	if inputs.LookupEnv == nil {
		return userMountResolution{}, errors.New("environment lookup is nil")
	}
	if err := launchplan.ValidateMountPath("host home directory", inputs.HomeDir); err != nil {
		return userMountResolution{}, err
	}

	codexHome, missingCodexHome, err := inspectCodexHome(inputs)
	if err != nil {
		return userMountResolution{}, err
	}

	mounts := UserMounts{CodexHome: codexHome}
	overlapSources := make([]string, 0, len(inputs.WritableSources)+1)
	overlapSources = append(overlapSources, inputs.WritableSources...)
	if mounts.CodexHomePresent() {
		overlapSources = append(overlapSources, codexHome)
	}

	personalSkills, err := resolvePersonalSkills(inputs.HomeDir, overlapSources, inputs.CodexHomePolicy)
	if err != nil {
		return userMountResolution{}, err
	}
	mounts.PersonalSkills = personalSkills

	return userMountResolution{mounts: mounts, missingCodexHome: missingCodexHome}, nil
}

func inspectCodexHome(inputs UserMountInputs) (string, string, error) {
	source := filepath.Join(inputs.HomeDir, ".codex")
	explicit := false
	if requested, ok := inputs.LookupEnv(codexHomeEnv); ok && strings.TrimSpace(requested) != "" {
		// Use the trimmed value the emptiness guard already accepted, so a CODEX_HOME carrying a
		// stray newline or space from command substitution resolves the real directory instead of
		// failing canonicalization or the absolute-path check on the untrimmed string.
		source = strings.TrimSpace(requested)
		explicit = true
		if !filepath.IsAbs(source) {
			return "", "", fmt.Errorf("%s %q is not absolute", codexHomeEnv, source)
		}
	}

	existence, err := probePath("Codex home", source)
	if err != nil {
		return "", "", err
	}
	switch existence {
	case pathBrokenSymlink:
		return "", "", fmt.Errorf("Codex home %q is a broken symlink", source)
	case pathExists:
		canonical, err := canonicalizeExistingDir(
			"Codex home",
			source,
			accessReadOK|accessWriteOK|accessExecOK,
		)
		return canonical, "", err
	case pathMissing:
		// Whether a missing home is an error, a creation offer, or simply absent is policy, decided
		// below.
	}

	if explicit {
		return "", "", fmt.Errorf("Codex home %q does not exist", source)
	}
	if inputs.CodexHomePolicy == CodexHomeOptional {
		// agents-safe runs commands that need no Codex state: mount the implicit default only if it
		// exists. An explicit missing CODEX_HOME was rejected above because it is user intent.
		return CodexHomeAbsent, "", nil
	}
	return CodexHomeAbsent, source, nil
}

func resolvePersonalSkills(
	homeDir string,
	writableSources []string,
	codexHomePolicy CodexHomePolicy,
) (string, error) {
	parent := filepath.Join(homeDir, ".agents")
	present, err := skillsPathPresent("personal-skills parent", "personal-skills parent", parent, codexHomePolicy)
	if err != nil {
		return "", err
	}
	if !present {
		return PersonalSkillsAbsent, nil
	}

	source := filepath.Join(parent, "skills")
	present, err = skillsPathPresent("personal skills", "personal-skills source", source, codexHomePolicy)
	if err != nil {
		return "", err
	}
	if !present {
		return PersonalSkillsAbsent, nil
	}

	canonical, err := canonicalizeExistingDir("personal skills", source, accessReadOK|accessExecOK)
	if err != nil {
		return "", err
	}

	for _, writable := range writableSources {
		if launchplan.PathsOverlap(canonical, writable) {
			return "", fmt.Errorf(
				"personal-skills source %q overlaps writable mount %q; a read-only skill must not be "+
					"modifiable through a writable alias",
				canonical,
				writable,
			)
		}
	}
	return canonical, nil
}

// pathExistence is what probePath learned about an optional host path.
type pathExistence int

const (
	// pathMissing means nothing exists at the path.
	pathMissing pathExistence = iota
	// pathExists means the path resolves to existing state.
	pathExists
	// pathBrokenSymlink means a symlink exists but its target does not. Callers decide whether that
	// is an error or, like a plain absence, just nothing to mount.
	pathBrokenSymlink
)

// probePath reports whether path exists, distinguishing a broken symlink — which Lstat sees but
// Stat does not — from a plain absence. inspectLabel names the path in I/O error messages.
func probePath(inspectLabel string, path string) (pathExistence, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return pathMissing, nil
		}
		return pathMissing, fmt.Errorf("inspect %s %q: %w", inspectLabel, path, err)
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) && info.Mode()&os.ModeSymlink != 0 {
			return pathBrokenSymlink, nil
		}
		return pathMissing, fmt.Errorf("inspect %s %q: %w", inspectLabel, path, err)
	}
	return pathExists, nil
}

// skillsPathPresent probes a personal-skills path. A broken symlink is user intent that cannot be
// honored, so it fails a launch that requires a Codex home; a policy that treats user state as
// optional reports it as absent instead, like any other missing skills path.
func skillsPathPresent(
	inspectLabel string,
	symlinkSubject string,
	path string,
	codexHomePolicy CodexHomePolicy,
) (bool, error) {
	existence, err := probePath(inspectLabel, path)
	if err != nil {
		return false, err
	}
	if existence == pathBrokenSymlink && codexHomePolicy != CodexHomeOptional {
		return false, fmt.Errorf("%s %q is a broken symlink", symlinkSubject, path)
	}
	return existence == pathExists, nil
}

// canonicalizeExistingDir resolves source to an existing canonical directory that Docker can bind
// and that grants accessMode to the invoking host user. It never creates the directory.
func canonicalizeExistingDir(label string, source string, accessMode uint32) (string, error) {
	if source == "" {
		return "", fmt.Errorf("%s is empty", label)
	}
	if !filepath.IsAbs(source) {
		return "", fmt.Errorf("%s %q is not absolute", label, source)
	}
	canonical, err := filepath.EvalSymlinks(source)
	if err != nil {
		return "", fmt.Errorf("canonicalize %s %q: %w", label, source, err)
	}
	canonical = filepath.Clean(canonical)
	if canonical == string(filepath.Separator) {
		return "", fmt.Errorf("%s cannot be the filesystem root", label)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("inspect %s %q: %w", label, canonical, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s %q is not a directory", label, canonical)
	}
	if err := launchplan.ValidateMountPath(label, canonical); err != nil {
		return "", err
	}
	if err := syscall.Access(canonical, accessMode); err != nil {
		return "", fmt.Errorf("%s %q is not accessible with the required permissions: %w", label, canonical, err)
	}
	return canonical, nil
}
