package launcher

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func envLookup(pairs map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := pairs[key]
		return value, ok
	}
}

func inspectExistingUserMounts(inputs UserMountInputs) (UserMounts, error) {
	resolution, err := inspectUserMounts(inputs)
	if err != nil {
		return UserMounts{}, err
	}
	if resolution.missingCodexHome != "" {
		return UserMounts{}, fmt.Errorf("Codex home %q still needs materialization", resolution.missingCodexHome)
	}
	return resolution.mounts, nil
}

func evalPath(t *testing.T, path string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q) error = %v", path, err)
	}
	return filepath.Clean(canonical)
}

func mkdir(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", path, err)
	}
	return path
}

func TestInspectUserMountsDefaultCodexHome(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	codexHome := mkdir(t, filepath.Join(home, ".codex"))

	state, err := inspectExistingUserMounts(UserMountInputs{
		LookupEnv: envLookup(nil),
		HomeDir:   evalPath(t, home),
	})
	if err != nil {
		t.Fatalf("inspectExistingUserMounts() error = %v", err)
	}
	if state.CodexHome != evalPath(t, codexHome) {
		t.Errorf("CodexHome = %q, want %q", state.CodexHome, evalPath(t, codexHome))
	}
	if state.PersonalSkills != PersonalSkillsAbsent {
		t.Errorf("PersonalSkills = %q, want %q", state.PersonalSkills, PersonalSkillsAbsent)
	}
	if state.SkillsPresent() {
		t.Error("SkillsPresent() = true, want false")
	}
}

func TestInspectUserMountsExplicitCodexHome(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	mkdir(t, filepath.Join(home, ".codex"))
	custom := mkdir(t, filepath.Join(t.TempDir(), "custom codex"))

	state, err := inspectExistingUserMounts(UserMountInputs{
		LookupEnv: envLookup(map[string]string{codexHomeEnv: custom}),
		HomeDir:   evalPath(t, home),
	})
	if err != nil {
		t.Fatalf("inspectExistingUserMounts() error = %v", err)
	}
	if state.CodexHome != evalPath(t, custom) {
		t.Errorf("CodexHome = %q, want explicit %q", state.CodexHome, evalPath(t, custom))
	}
}

func TestInspectUserMountsEmptyCodexHomeFallsBackToDefault(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	codexHome := mkdir(t, filepath.Join(home, ".codex"))

	state, err := inspectExistingUserMounts(UserMountInputs{
		LookupEnv: envLookup(map[string]string{codexHomeEnv: "   "}),
		HomeDir:   evalPath(t, home),
	})
	if err != nil {
		t.Fatalf("inspectExistingUserMounts() error = %v", err)
	}
	if state.CodexHome != evalPath(t, codexHome) {
		t.Errorf("CodexHome = %q, want default %q", state.CodexHome, evalPath(t, codexHome))
	}
}

func TestInspectUserMountsTrimsCodexHomeWhitespace(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	custom := mkdir(t, filepath.Join(t.TempDir(), "custom codex"))

	// A CODEX_HOME captured through command substitution often carries a trailing newline or a
	// leading space; the launcher must resolve the real directory rather than reject it.
	for _, raw := range []string{custom + "\n", " " + custom, "\t" + custom + " "} {
		state, err := inspectExistingUserMounts(UserMountInputs{
			LookupEnv: envLookup(map[string]string{codexHomeEnv: raw}),
			HomeDir:   evalPath(t, home),
		})
		if err != nil {
			t.Fatalf("inspectExistingUserMounts(CODEX_HOME=%q) error = %v", raw, err)
		}
		if state.CodexHome != evalPath(t, custom) {
			t.Errorf("CodexHome = %q, want trimmed %q", state.CodexHome, evalPath(t, custom))
		}
	}
}

func TestInspectUserMountsOptionalCodexHomeAbsent(t *testing.T) {
	t.Parallel()
	home := evalPath(t, t.TempDir()) // no .codex created

	state, err := inspectExistingUserMounts(UserMountInputs{
		LookupEnv:       envLookup(nil),
		HomeDir:         home,
		CodexHomePolicy: CodexHomeOptional,
	})
	if err != nil {
		t.Fatalf("inspectExistingUserMounts() error = %v", err)
	}
	if state.CodexHome != CodexHomeAbsent {
		t.Errorf("CodexHome = %q, want %q", state.CodexHome, CodexHomeAbsent)
	}
	if state.CodexHomePresent() {
		t.Error("CodexHomePresent() = true, want false for an optional missing home")
	}
}

func TestInspectUserMountsOptionalExplicitMissingErrors(t *testing.T) {
	t.Parallel()
	home := evalPath(t, t.TempDir())
	missing := filepath.Join(t.TempDir(), "missing codex")

	_, err := inspectExistingUserMounts(UserMountInputs{
		LookupEnv:       envLookup(map[string]string{codexHomeEnv: missing}),
		HomeDir:         home,
		CodexHomePolicy: CodexHomeOptional,
	})
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("inspectExistingUserMounts() error = %v, want missing explicit CODEX_HOME rejection", err)
	}
}

func TestInspectUserMountsRequiredMissingDefaultNeedsMaterialization(t *testing.T) {
	t.Parallel()
	home := evalPath(t, t.TempDir())
	wantHome := filepath.Join(home, ".codex")

	resolution, err := inspectUserMounts(UserMountInputs{
		LookupEnv:       envLookup(nil),
		HomeDir:         home,
		CodexHomePolicy: CodexHomeRequired,
	})
	if err != nil {
		t.Fatalf("inspectUserMounts() error = %v", err)
	}
	if resolution.mounts.CodexHome != CodexHomeAbsent {
		t.Errorf("CodexHome = %q, want %q before materialization", resolution.mounts.CodexHome, CodexHomeAbsent)
	}
	if resolution.missingCodexHome != wantHome {
		t.Errorf("missingCodexHome = %q, want %q", resolution.missingCodexHome, wantHome)
	}
}

func TestInspectUserMountsRequiredExplicitMissingErrors(t *testing.T) {
	t.Parallel()
	home := evalPath(t, t.TempDir())
	mkdir(t, filepath.Join(home, ".codex")) // default exists; the explicit source does not
	missing := filepath.Join(t.TempDir(), "missing codex")

	_, err := inspectExistingUserMounts(UserMountInputs{
		LookupEnv:       envLookup(map[string]string{codexHomeEnv: missing}),
		HomeDir:         home,
		CodexHomePolicy: CodexHomeRequired,
	})
	if err == nil {
		t.Fatal("inspectExistingUserMounts() error = nil, want rejection of a missing explicit CODEX_HOME")
	}
}

func TestInspectUserMountsCanonicalizesSymlinkedCodexHome(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	mkdir(t, filepath.Join(home, ".codex"))
	real := mkdir(t, filepath.Join(t.TempDir(), "real codex"))
	link := filepath.Join(t.TempDir(), "link-codex")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	state, err := inspectExistingUserMounts(UserMountInputs{
		LookupEnv: envLookup(map[string]string{codexHomeEnv: link}),
		HomeDir:   evalPath(t, home),
	})
	if err != nil {
		t.Fatalf("inspectExistingUserMounts() error = %v", err)
	}
	if state.CodexHome != evalPath(t, real) {
		t.Errorf("CodexHome = %q, want symlink target %q", state.CodexHome, evalPath(t, real))
	}
}

func TestInspectUserMountsRejectsDanglingDefaultCodexHomeSymlink(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	link := filepath.Join(home, ".codex")
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing target"), link); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	_, err := inspectExistingUserMounts(UserMountInputs{
		LookupEnv: envLookup(nil),
		HomeDir:   evalPath(t, home),
	})
	if err == nil || !strings.Contains(err.Error(), "broken symlink") {
		t.Fatalf("inspectExistingUserMounts() error = %v, want broken Codex-home symlink rejection", err)
	}
}

func TestInspectUserMountsRejectsInvalidCodexHome(t *testing.T) {
	t.Parallel()
	home := evalPath(t, t.TempDir())
	nonDir := filepath.Join(t.TempDir(), "codex-file")
	if err := os.WriteFile(nonDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	tests := []struct {
		name    string
		codex   string
		useEnv  bool
		homeDir string
		want    string
	}{
		{name: "relative", useEnv: true, codex: "relative/codex", homeDir: home, want: "is not absolute"},
		{name: "filesystem root", useEnv: true, codex: "/", homeDir: home, want: "filesystem root"},
		{name: "non-directory", useEnv: true, codex: nonDir, homeDir: home, want: "is not a directory"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			pairs := map[string]string{}
			if test.useEnv {
				pairs[codexHomeEnv] = test.codex
			}
			_, err := inspectExistingUserMounts(UserMountInputs{
				LookupEnv: envLookup(pairs),
				HomeDir:   test.homeDir,
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("inspectExistingUserMounts() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestInspectUserMountsRejectsUnwritableCodexHome(t *testing.T) {
	t.Parallel()
	if os.Getuid() == 0 {
		t.Skip("root bypasses access(2) permission bits")
	}
	home := t.TempDir()
	codexHome := filepath.Join(home, ".codex")
	if err := os.Mkdir(codexHome, 0o500); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(codexHome, 0o700) })

	_, err := inspectExistingUserMounts(UserMountInputs{
		LookupEnv: envLookup(nil),
		HomeDir:   evalPath(t, home),
	})
	if err == nil || !strings.Contains(err.Error(), "not accessible") {
		t.Fatalf("inspectExistingUserMounts() error = %v, want unwritable rejection", err)
	}
}

func TestInspectUserMountsResolvesPresentSkills(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	mkdir(t, filepath.Join(home, ".codex"))
	skills := mkdir(t, filepath.Join(home, ".agents", "skills"))
	unrelated := evalPath(t, mkdir(t, filepath.Join(t.TempDir(), "worktree")))

	state, err := inspectExistingUserMounts(UserMountInputs{
		LookupEnv:       envLookup(nil),
		HomeDir:         evalPath(t, home),
		WritableSources: []string{unrelated},
	})
	if err != nil {
		t.Fatalf("inspectExistingUserMounts() error = %v", err)
	}
	if state.PersonalSkills != evalPath(t, skills) {
		t.Errorf("PersonalSkills = %q, want %q", state.PersonalSkills, evalPath(t, skills))
	}
	if !state.SkillsPresent() {
		t.Error("SkillsPresent() = false, want true")
	}
}

func TestInspectUserMountsRejectsDanglingSkillsSymlink(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	mkdir(t, filepath.Join(home, ".codex"))
	skillsLink := filepath.Join(home, ".agents", "skills")
	mkdir(t, filepath.Dir(skillsLink))
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing target"), skillsLink); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	_, err := inspectExistingUserMounts(UserMountInputs{
		LookupEnv: envLookup(nil),
		HomeDir:   evalPath(t, home),
	})
	if err == nil || !strings.Contains(err.Error(), "broken symlink") {
		t.Fatalf("inspectExistingUserMounts() error = %v, want broken-symlink rejection", err)
	}
}

func TestInspectUserMountsOptionalIgnoresDanglingSkillsSymlink(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	skillsLink := filepath.Join(home, ".agents", "skills")
	mkdir(t, filepath.Dir(skillsLink))
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing target"), skillsLink); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	state, err := inspectExistingUserMounts(UserMountInputs{
		LookupEnv:       envLookup(nil),
		HomeDir:         evalPath(t, home),
		CodexHomePolicy: CodexHomeOptional,
	})
	if err != nil {
		t.Fatalf("inspectExistingUserMounts() error = %v", err)
	}
	if state.PersonalSkills != PersonalSkillsAbsent {
		t.Fatalf("PersonalSkills = %q, want %q", state.PersonalSkills, PersonalSkillsAbsent)
	}
}

func TestInspectUserMountsRejectsDanglingSkillsParentSymlink(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	mkdir(t, filepath.Join(home, ".codex"))
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing target"), filepath.Join(home, ".agents")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	_, err := inspectExistingUserMounts(UserMountInputs{
		LookupEnv: envLookup(nil),
		HomeDir:   evalPath(t, home),
	})
	if err == nil || !strings.Contains(err.Error(), "personal-skills parent") ||
		!strings.Contains(err.Error(), "broken symlink") {
		t.Fatalf("inspectExistingUserMounts() error = %v, want broken parent-symlink rejection", err)
	}
}

func TestInspectUserMountsOptionalIgnoresDanglingSkillsParentSymlink(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing target"), filepath.Join(home, ".agents")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	state, err := inspectExistingUserMounts(UserMountInputs{
		LookupEnv:       envLookup(nil),
		HomeDir:         evalPath(t, home),
		CodexHomePolicy: CodexHomeOptional,
	})
	if err != nil {
		t.Fatalf("inspectExistingUserMounts() error = %v", err)
	}
	if state.PersonalSkills != PersonalSkillsAbsent {
		t.Fatalf("PersonalSkills = %q, want %q", state.PersonalSkills, PersonalSkillsAbsent)
	}
}

func TestInspectUserMountsRejectsSkillsOverlappingWritableSource(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	mkdir(t, filepath.Join(home, ".codex"))
	mkdir(t, filepath.Join(home, ".agents", "skills"))
	canonicalHome := evalPath(t, home)

	// A literal overlap: a writable mount source is an ancestor of the skills directory.
	_, err := inspectExistingUserMounts(UserMountInputs{
		LookupEnv:       envLookup(nil),
		HomeDir:         canonicalHome,
		WritableSources: []string{canonicalHome},
	})
	if err == nil || !strings.Contains(err.Error(), "overlaps writable mount") {
		t.Fatalf("inspectExistingUserMounts() error = %v, want overlap rejection", err)
	}
}

func TestInspectUserMountsRejectsSkillsOverlappingViaSymlink(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	mkdir(t, filepath.Join(home, ".codex"))
	worktree := evalPath(t, mkdir(t, filepath.Join(t.TempDir(), "worktree")))
	realSkills := mkdir(t, filepath.Join(worktree, "checked-in-skills"))
	skillsLink := filepath.Join(home, ".agents", "skills")
	mkdir(t, filepath.Dir(skillsLink))
	if err := os.Symlink(realSkills, skillsLink); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	_, err := inspectExistingUserMounts(UserMountInputs{
		LookupEnv:       envLookup(nil),
		HomeDir:         evalPath(t, home),
		WritableSources: []string{worktree},
	})
	if err == nil || !strings.Contains(err.Error(), "overlaps writable mount") {
		t.Fatalf("inspectExistingUserMounts() error = %v, want symlinked overlap rejection", err)
	}
}

func TestInspectUserMountsRejectsWritableSourceNestedUnderSkills(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	mkdir(t, filepath.Join(home, ".codex"))
	skills := mkdir(t, filepath.Join(home, ".agents", "skills"))
	// A writable mount source nested under the skills directory: overlap must be caught in the
	// reverse containment direction (the read-only skills source contains a writable source).
	nestedWritable := evalPath(t, mkdir(t, filepath.Join(skills, "nested-writable")))

	_, err := inspectExistingUserMounts(UserMountInputs{
		LookupEnv:       envLookup(nil),
		HomeDir:         evalPath(t, home),
		WritableSources: []string{nestedWritable},
	})
	if err == nil || !strings.Contains(err.Error(), "overlaps writable mount") {
		t.Fatalf("inspectExistingUserMounts() error = %v, want reverse-containment overlap rejection", err)
	}
}

func TestInspectUserMountsRejectsSkillsOverlappingCodexHome(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	// Point CODEX_HOME at $HOME/.agents so the skills directory nests inside the writable Codex home.
	codexHome := mkdir(t, filepath.Join(home, ".agents"))
	mkdir(t, filepath.Join(codexHome, "skills"))

	_, err := inspectExistingUserMounts(UserMountInputs{
		LookupEnv: envLookup(map[string]string{codexHomeEnv: codexHome}),
		HomeDir:   evalPath(t, home),
	})
	if err == nil || !strings.Contains(err.Error(), "overlaps writable mount") {
		t.Fatalf("inspectExistingUserMounts() error = %v, want Codex-home overlap rejection", err)
	}
}
