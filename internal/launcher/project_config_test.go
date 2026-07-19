package launcher

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
)

func TestDefaultProjectConfigUsesHostAndGitTopology(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	home := filepath.Join(base, "home")
	primary := filepath.Join(base, "primary")
	worktree := filepath.Join(base, "worktree")
	commonGit := filepath.Join(primary, ".git")
	for _, path := range []string{home, primary, worktree, commonGit} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gitConfig := filepath.Join(home, ".gitconfig")
	if err := os.WriteFile(gitConfig, []byte("[user]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	codexHome := filepath.Join(home, ".codex")
	skills := filepath.Join(home, ".agents", "skills")
	for _, path := range []string{codexHome, skills} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	project := gitproject.Project{
		RequestedDir: worktree,
		WorktreeRoot: worktree,
		PrimaryRoot:  primary,
		CommonGitDir: commonGit,
		Linked:       true,
	}
	config, err := DefaultProjectConfig(project, HostEnvironment{
		HomeDir:        home,
		GitConfig:      gitConfig,
		CodexHome:      codexHome,
		PersonalSkills: skills,
	}, "test:image")
	if err != nil {
		t.Fatal(err)
	}
	roles := make(map[string]projectenv.MountConfig, len(config.Common.Mounts))
	for _, mount := range config.Common.Mounts {
		roles[mount.Role] = mount
	}
	if mount := roles[projectenv.RolePrimaryCheckout]; mount.ReadOnly != true || mount.Source != primary {
		t.Errorf("primary mount = %#v, want read-only %q", mount, primary)
	}
	if mount := roles[projectenv.RoleCommonGitDir]; mount.Source != commonGit || mount.ReadOnly {
		t.Errorf("common Git mount = %#v, want writable %q", mount, commonGit)
	}
	if mount := roles[projectenv.RoleCodexHome]; mount.Target != filepath.Join(home, ".codex") {
		t.Errorf("Codex mount = %#v, want target below home", mount)
	}
	if mount := roles[projectenv.RoleHostMCPChannel]; mount.Source != projectenv.HostMCPChannelSource {
		t.Errorf("host MCP mount = %#v, want logical source", mount)
	}
	if want := []string{"--sandbox", "danger-full-access"}; !reflect.DeepEqual(config.Codex.Arguments, want) {
		t.Errorf("arguments = %#v, want %#v", config.Codex.Arguments, want)
	}
}

func TestResolveHostEnvironmentUsesOneCanonicalOptionalSnapshot(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	codexHome := filepath.Join(home, ".codex")
	skills := filepath.Join(home, ".agents", "skills")
	for _, path := range []string{codexHome, skills} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gitConfig := filepath.Join(home, ".gitconfig")
	if err := os.WriteFile(gitConfig, []byte("[user]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	environment, err := resolveHostEnvironment(HostEnvironmentInputs{
		UserHomeDir: func() (string, error) { return home, nil },
		LookupEnv:   envLookup(nil),
		DiscoverGitConfig: func(gotHome string) (string, error) {
			if gotHome != home {
				t.Errorf("home = %q, want %q", gotHome, home)
			}
			return gitConfig, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if environment.HomeDir != home || environment.GitConfig != gitConfig || environment.CodexHome != codexHome ||
		environment.PersonalSkills != skills {
		t.Fatalf("HostEnvironment = %#v, want paths below %q", environment, home)
	}
}

func TestResolveHostEnvironmentRejectsIncompleteInputs(t *testing.T) {
	t.Parallel()
	_, err := resolveHostEnvironment(HostEnvironmentInputs{UserHomeDir: func() (string, error) {
		return "", errors.New("unexpected")
	}})
	if err == nil || err.Error() != "host environment inputs are incomplete" {
		t.Fatalf("resolveHostEnvironment() error = %v, want incomplete inputs", err)
	}
}
