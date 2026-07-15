package launcher

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// codexHomeEnv is the host environment variable that overrides the default Codex home.
const codexHomeEnv = "CODEX_HOME"

// PersonalSkillsAbsent is recorded as the personal-skills source when the host has no
// $HOME/.agents/skills directory. It is also the codex-safe.personal-skills label value in that
// case, so reuse can prove a running container was created with the same user state.
const PersonalSkillsAbsent = "absent"

// POSIX access(2) mode bits. The launcher supports only Linux, where these values are stable.
const (
	accessExecOK  = 0x1
	accessWriteOK = 0x2
	accessReadOK  = 0x4
)

// UserState holds the resolved host sources mounted for one launch's Codex user state.
type UserState struct {
	// CodexHome is the canonical host directory mounted read-write as the container Codex home.
	CodexHome string
	// PersonalSkills is the canonical host $HOME/.agents/skills directory mounted read-only, or
	// PersonalSkillsAbsent when the host has no such directory.
	PersonalSkills string
}

// SkillsPresent reports whether a personal-skills directory was resolved for this launch.
func (state UserState) SkillsPresent() bool {
	return state.PersonalSkills != "" && state.PersonalSkills != PersonalSkillsAbsent
}

// UserStateInputs carries the host inputs needed to resolve launch user state.
type UserStateInputs struct {
	// LookupEnv reads host environment variables; it is os.LookupEnv in production.
	LookupEnv func(string) (string, bool)
	// HomeDir is the canonical absolute host home directory.
	HomeDir string
	// WritableSources are the canonical read-write host mount sources besides the Codex home
	// (the worktree root and, for a linked worktree, the common Git directory). A personal-skills
	// source overlapping any of them, or the resolved Codex home, is rejected so a read-only skill
	// cannot be modified through a writable alias.
	WritableSources []string
}

// ResolveUserState resolves the Codex home and optional personal skills for one launch. It never
// creates a missing source and never falls back to another location.
func ResolveUserState(inputs UserStateInputs) (UserState, error) {
	if inputs.LookupEnv == nil {
		return UserState{}, errors.New("environment lookup is nil")
	}
	if err := validateMountPath("host home directory", inputs.HomeDir); err != nil {
		return UserState{}, err
	}

	codexHome, err := resolveCodexHome(inputs.LookupEnv, inputs.HomeDir)
	if err != nil {
		return UserState{}, err
	}

	overlapSources := make([]string, 0, len(inputs.WritableSources)+1)
	overlapSources = append(overlapSources, inputs.WritableSources...)
	overlapSources = append(overlapSources, codexHome)

	personalSkills, err := resolvePersonalSkills(inputs.HomeDir, overlapSources)
	if err != nil {
		return UserState{}, err
	}

	return UserState{CodexHome: codexHome, PersonalSkills: personalSkills}, nil
}

func resolveCodexHome(lookupEnv func(string) (string, bool), homeDir string) (string, error) {
	source := filepath.Join(homeDir, ".codex")
	if requested, ok := lookupEnv(codexHomeEnv); ok && strings.TrimSpace(requested) != "" {
		source = requested
		if !filepath.IsAbs(source) {
			return "", fmt.Errorf("%s %q is not absolute", codexHomeEnv, source)
		}
	}
	return canonicalizeExistingDir("Codex home", source, accessReadOK|accessWriteOK|accessExecOK)
}

func resolvePersonalSkills(homeDir string, writableSources []string) (string, error) {
	source := filepath.Join(homeDir, ".agents", "skills")
	if _, err := os.Stat(source); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return PersonalSkillsAbsent, nil
		}
		return "", fmt.Errorf("inspect personal skills %q: %w", source, err)
	}

	canonical, err := canonicalizeExistingDir("personal skills", source, accessReadOK|accessExecOK)
	if err != nil {
		return "", err
	}

	for _, writable := range writableSources {
		if pathsOverlap(canonical, writable) {
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
	if err := validateMountPath(label, canonical); err != nil {
		return "", err
	}
	if err := syscall.Access(canonical, accessMode); err != nil {
		return "", fmt.Errorf("%s %q is not accessible with the required permissions: %w", label, canonical, err)
	}
	return canonical, nil
}

// pathsOverlap reports whether either path contains the other or the two are equal. Both inputs
// must be canonical absolute paths.
func pathsOverlap(a string, b string) bool {
	return isWithin(a, b) || isWithin(b, a)
}

func isWithin(child string, parent string) bool {
	if child == parent {
		return true
	}
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
