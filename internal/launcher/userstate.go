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

// CodexHomeAbsent is recorded as the Codex-home source when a CodexHomeOptional launch finds no
// Codex home to mount. It is also the codex-safe.codex-home label value in that case, so reuse can
// prove a running container was created with the same (absent) Codex state.
const CodexHomeAbsent = "absent"

// CodexHomePolicy controls how a missing Codex home is handled during user-state resolution.
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

// UserState holds the resolved host sources mounted for one launch's Codex user state.
type UserState struct {
	// CodexHome is the canonical host directory mounted read-write as the container Codex home, or
	// CodexHomeAbsent when an optional launch resolved no Codex home.
	CodexHome string
	// PersonalSkills is the canonical host $HOME/.agents/skills directory mounted read-only, or
	// PersonalSkillsAbsent when the host has no such directory.
	PersonalSkills string
}

// SkillsPresent reports whether a personal-skills directory was resolved for this launch.
func (state UserState) SkillsPresent() bool {
	return state.PersonalSkills != "" && state.PersonalSkills != PersonalSkillsAbsent
}

// CodexHomePresent reports whether a Codex home was resolved and should be mounted for this launch.
func (state UserState) CodexHomePresent() bool {
	return state.CodexHome != "" && state.CodexHome != CodexHomeAbsent
}

// codexHomeLabel returns the codex-safe.codex-home label value: the canonical source, or the absent
// marker when the launch resolved no Codex home.
func (state UserState) codexHomeLabel() string {
	if !state.CodexHomePresent() {
		return CodexHomeAbsent
	}
	return state.CodexHome
}

// personalSkillsLabel returns the codex-safe.personal-skills label value: the canonical source, or
// the literal absent marker when no personal-skills directory was resolved.
func (state UserState) personalSkillsLabel() string {
	if state.PersonalSkills == "" {
		return PersonalSkillsAbsent
	}
	return state.PersonalSkills
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
	// CodexHomePolicy controls how a missing Codex home is handled. The zero value is
	// CodexHomeRequired.
	CodexHomePolicy CodexHomePolicy
}

type userStateResolution struct {
	state            UserState
	missingCodexHome string
}

// inspectUserState resolves existing sources without prompting or creating host state. A missing
// required implicit default is returned separately so the launcher can defer confirmation until
// Docker preflight and deterministic-container reuse checks have succeeded.
func inspectUserState(inputs UserStateInputs) (userStateResolution, error) {
	if inputs.LookupEnv == nil {
		return userStateResolution{}, errors.New("environment lookup is nil")
	}
	if err := validateMountPath("host home directory", inputs.HomeDir); err != nil {
		return userStateResolution{}, err
	}

	codexHome, missingCodexHome, err := inspectCodexHome(inputs)
	if err != nil {
		return userStateResolution{}, err
	}

	state := UserState{CodexHome: codexHome}
	overlapSources := make([]string, 0, len(inputs.WritableSources)+1)
	overlapSources = append(overlapSources, inputs.WritableSources...)
	if state.CodexHomePresent() {
		overlapSources = append(overlapSources, codexHome)
	}

	personalSkills, err := resolvePersonalSkills(inputs.HomeDir, overlapSources, inputs.CodexHomePolicy)
	if err != nil {
		return userStateResolution{}, err
	}
	state.PersonalSkills = personalSkills

	return userStateResolution{state: state, missingCodexHome: missingCodexHome}, nil
}

func inspectCodexHome(inputs UserStateInputs) (string, string, error) {
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

	info, lstatErr := os.Lstat(source)
	if lstatErr == nil {
		if _, err := os.Stat(source); err != nil {
			if errors.Is(err, os.ErrNotExist) && info.Mode()&os.ModeSymlink != 0 {
				return "", "", fmt.Errorf("Codex home %q is a broken symlink", source)
			}
			return "", "", fmt.Errorf("inspect Codex home %q: %w", source, err)
		}
		canonical, err := canonicalizeExistingDir(
			"Codex home",
			source,
			accessReadOK|accessWriteOK|accessExecOK,
		)
		return canonical, "", err
	}
	if !errors.Is(lstatErr, os.ErrNotExist) {
		return "", "", fmt.Errorf("inspect Codex home %q: %w", source, lstatErr)
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
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return PersonalSkillsAbsent, nil
		}
		return "", fmt.Errorf("inspect personal-skills parent %q: %w", parent, err)
	}
	if _, err := os.Stat(parent); err != nil {
		if errors.Is(err, os.ErrNotExist) && parentInfo.Mode()&os.ModeSymlink != 0 {
			if codexHomePolicy == CodexHomeOptional {
				return PersonalSkillsAbsent, nil
			}
			return "", fmt.Errorf("personal-skills parent %q is a broken symlink", parent)
		}
		return "", fmt.Errorf("inspect personal-skills parent %q: %w", parent, err)
	}

	source := filepath.Join(parent, "skills")
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return PersonalSkillsAbsent, nil
		}
		return "", fmt.Errorf("inspect personal skills %q: %w", source, err)
	}
	if _, err := os.Stat(source); err != nil {
		if errors.Is(err, os.ErrNotExist) && sourceInfo.Mode()&os.ModeSymlink != 0 {
			if codexHomePolicy == CodexHomeOptional {
				return PersonalSkillsAbsent, nil
			}
			return "", fmt.Errorf("personal-skills source %q is a broken symlink", source)
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
