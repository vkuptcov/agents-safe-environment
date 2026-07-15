package launcher

import (
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

func TestResolveUserStateDefaultCodexHome(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	codexHome := mkdir(t, filepath.Join(home, ".codex"))

	state, err := ResolveUserState(UserStateInputs{
		LookupEnv: envLookup(nil),
		HomeDir:   evalPath(t, home),
	})
	if err != nil {
		t.Fatalf("ResolveUserState() error = %v", err)
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

func TestResolveUserStateExplicitCodexHome(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	mkdir(t, filepath.Join(home, ".codex"))
	custom := mkdir(t, filepath.Join(t.TempDir(), "custom codex"))

	state, err := ResolveUserState(UserStateInputs{
		LookupEnv: envLookup(map[string]string{codexHomeEnv: custom}),
		HomeDir:   evalPath(t, home),
	})
	if err != nil {
		t.Fatalf("ResolveUserState() error = %v", err)
	}
	if state.CodexHome != evalPath(t, custom) {
		t.Errorf("CodexHome = %q, want explicit %q", state.CodexHome, evalPath(t, custom))
	}
}

func TestResolveUserStateEmptyCodexHomeFallsBackToDefault(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	codexHome := mkdir(t, filepath.Join(home, ".codex"))

	state, err := ResolveUserState(UserStateInputs{
		LookupEnv: envLookup(map[string]string{codexHomeEnv: "   "}),
		HomeDir:   evalPath(t, home),
	})
	if err != nil {
		t.Fatalf("ResolveUserState() error = %v", err)
	}
	if state.CodexHome != evalPath(t, codexHome) {
		t.Errorf("CodexHome = %q, want default %q", state.CodexHome, evalPath(t, codexHome))
	}
}

func TestResolveUserStateCanonicalizesSymlinkedCodexHome(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	mkdir(t, filepath.Join(home, ".codex"))
	real := mkdir(t, filepath.Join(t.TempDir(), "real codex"))
	link := filepath.Join(t.TempDir(), "link-codex")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	state, err := ResolveUserState(UserStateInputs{
		LookupEnv: envLookup(map[string]string{codexHomeEnv: link}),
		HomeDir:   evalPath(t, home),
	})
	if err != nil {
		t.Fatalf("ResolveUserState() error = %v", err)
	}
	if state.CodexHome != evalPath(t, real) {
		t.Errorf("CodexHome = %q, want symlink target %q", state.CodexHome, evalPath(t, real))
	}
}

func TestResolveUserStateRejectsInvalidCodexHome(t *testing.T) {
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
		{name: "missing default", useEnv: false, homeDir: home, want: "canonicalize Codex home"},
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
			_, err := ResolveUserState(UserStateInputs{
				LookupEnv: envLookup(pairs),
				HomeDir:   test.homeDir,
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ResolveUserState() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestResolveUserStateRejectsUnwritableCodexHome(t *testing.T) {
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

	_, err := ResolveUserState(UserStateInputs{
		LookupEnv: envLookup(nil),
		HomeDir:   evalPath(t, home),
	})
	if err == nil || !strings.Contains(err.Error(), "not accessible") {
		t.Fatalf("ResolveUserState() error = %v, want unwritable rejection", err)
	}
}

func TestResolveUserStateResolvesPresentSkills(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	mkdir(t, filepath.Join(home, ".codex"))
	skills := mkdir(t, filepath.Join(home, ".agents", "skills"))
	unrelated := evalPath(t, mkdir(t, filepath.Join(t.TempDir(), "worktree")))

	state, err := ResolveUserState(UserStateInputs{
		LookupEnv:       envLookup(nil),
		HomeDir:         evalPath(t, home),
		WritableSources: []string{unrelated},
	})
	if err != nil {
		t.Fatalf("ResolveUserState() error = %v", err)
	}
	if state.PersonalSkills != evalPath(t, skills) {
		t.Errorf("PersonalSkills = %q, want %q", state.PersonalSkills, evalPath(t, skills))
	}
	if !state.SkillsPresent() {
		t.Error("SkillsPresent() = false, want true")
	}
}

func TestResolveUserStateRejectsSkillsOverlappingWritableSource(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	mkdir(t, filepath.Join(home, ".codex"))
	mkdir(t, filepath.Join(home, ".agents", "skills"))
	canonicalHome := evalPath(t, home)

	// A literal overlap: a writable mount source is an ancestor of the skills directory.
	_, err := ResolveUserState(UserStateInputs{
		LookupEnv:       envLookup(nil),
		HomeDir:         canonicalHome,
		WritableSources: []string{canonicalHome},
	})
	if err == nil || !strings.Contains(err.Error(), "overlaps writable mount") {
		t.Fatalf("ResolveUserState() error = %v, want overlap rejection", err)
	}
}

func TestResolveUserStateRejectsSkillsOverlappingViaSymlink(t *testing.T) {
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

	_, err := ResolveUserState(UserStateInputs{
		LookupEnv:       envLookup(nil),
		HomeDir:         evalPath(t, home),
		WritableSources: []string{worktree},
	})
	if err == nil || !strings.Contains(err.Error(), "overlaps writable mount") {
		t.Fatalf("ResolveUserState() error = %v, want symlinked overlap rejection", err)
	}
}

func TestResolveUserStateRejectsWritableSourceNestedUnderSkills(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	mkdir(t, filepath.Join(home, ".codex"))
	skills := mkdir(t, filepath.Join(home, ".agents", "skills"))
	// A writable mount source nested under the skills directory: overlap must be caught in the
	// reverse containment direction (the read-only skills source contains a writable source).
	nestedWritable := evalPath(t, mkdir(t, filepath.Join(skills, "nested-writable")))

	_, err := ResolveUserState(UserStateInputs{
		LookupEnv:       envLookup(nil),
		HomeDir:         evalPath(t, home),
		WritableSources: []string{nestedWritable},
	})
	if err == nil || !strings.Contains(err.Error(), "overlaps writable mount") {
		t.Fatalf("ResolveUserState() error = %v, want reverse-containment overlap rejection", err)
	}
}

func TestResolveUserStateRejectsSkillsOverlappingCodexHome(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	// Point CODEX_HOME at $HOME/.agents so the skills directory nests inside the writable Codex home.
	codexHome := mkdir(t, filepath.Join(home, ".agents"))
	mkdir(t, filepath.Join(codexHome, "skills"))

	_, err := ResolveUserState(UserStateInputs{
		LookupEnv: envLookup(map[string]string{codexHomeEnv: codexHome}),
		HomeDir:   evalPath(t, home),
	})
	if err == nil || !strings.Contains(err.Error(), "overlaps writable mount") {
		t.Fatalf("ResolveUserState() error = %v, want Codex-home overlap rejection", err)
	}
}
