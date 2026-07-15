package smoke_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	sentinelModel        = "codex-safe-sentinel-model"
	agentsMarker         = "CODEX_SAFE_AGENTS_SENTINEL"
	codexSkillMarker     = "CODEX_SAFE_CODEX_SKILL_SENTINEL"
	personalSkillMarker  = "CODEX_SAFE_PERSONAL_SKILL_SENTINEL"
	externalSecretMarker = "CODEX_SAFE_EXTERNAL_SECRET"
	credentialedEnv      = "CODEX_SAFE_CREDENTIALED_ACCEPTANCE"
	credentialSourceEnv  = "CODEX_SAFE_TEST_AUTH_JSON"
)

// codexSentinel records host paths a Codex product launch is expected to read and write.
type codexSentinel struct {
	otherCodexHome string
	inspectReport  string
	personalSkills string
	externalTarget string
	fakeBinary     string
}

// TestSysboxCodexProductLaunch proves the product launcher runs Codex from a mounted sentinel Codex
// home on a real Sysbox host: it reads mounted configuration, writes host-owned session state,
// exposes personal skills read-only without their external symlink target, cannot be shadowed by a
// host Codex binary under the mounted state, and rejects a reuse with a different Codex home.
func TestSysboxCodexProductLaunch(t *testing.T) {
	if os.Getenv(goSmokeEnv) != "1" {
		t.Skipf("set %s=1 to run the real Sysbox Codex test", goSmokeEnv)
	}
	fixture := newSmokeFixture(t)
	if _, err := os.Stat(fixture.launcher.productBinary); err != nil {
		t.Skip("bin/codex-safe is missing; run make build first")
	}
	sentinel := fixture.setupCodexSentinel()

	// Hold the outer container open with a registered probe command so the product launches below
	// reuse one session created with the sentinel Codex home.
	holdRelease := filepath.Join(fixture.project.worktree, "codex-hold.release")
	hold := fixture.launcher.start(fixture.project.worktree, "bash", "-c", waitScript, "bash", holdRelease)
	fixture.docker.waitForOuter()

	fixture.assertCodexHomeMounts(sentinel)
	fixture.assertCodexReadsSentinelAndWritesState()
	fixture.assertImageCodexNotShadowed(sentinel)
	fixture.assertReuseMismatchDiagnostic(sentinel, hold)

	require.NoError(t, os.WriteFile(holdRelease, nil, 0o600), "hold release marker must be writable")
	hold.requireExit(t, "codex hold command")
	fixture.docker.waitForOuterRemoval()
	requireHostOwnership(t, fixture.project.codexHome, sentinel.personalSkills)
}

func (fixture *smokeFixture) setupCodexSentinel() codexSentinel {
	fixture.t.Helper()
	home := fixture.project.codexHome

	writeFile(fixture.t, filepath.Join(home, "config.toml"),
		"model = \""+sentinelModel+"\"\napproval_policy = \"never\"\n")
	writeFile(fixture.t, filepath.Join(home, "AGENTS.md"), agentsMarker+"\n")
	writeFile(fixture.t, filepath.Join(home, "skills", "sentinel-skill", "SKILL.md"),
		"---\nname: sentinel-skill\n---\n"+codexSkillMarker+"\n")

	// A host Codex binary under the mounted state must remain inert data: the launcher invokes the
	// image-owned absolute path, never a bare name resolved through this directory.
	fakeBinary := filepath.Join(home, "bin", "codex")
	writeFile(fixture.t, fakeBinary, "#!/bin/sh\necho FAKE_CODEX_SHADOW\nexit 0\n")
	require.NoError(fixture.t, os.Chmod(fakeBinary, 0o755), "fake host Codex binary must be executable")

	personalSkills := filepath.Join(fixture.project.hostHome, ".agents", "skills")
	writeFile(fixture.t, filepath.Join(personalSkills, "personal-sentinel", "SKILL.md"),
		"---\nname: personal-sentinel\n---\n"+personalSkillMarker+"\n")

	// An external symlink target outside every mount must stay unavailable inside the container.
	externalTarget := filepath.Join(fixture.project.root, "secret outside mounts")
	writeFile(fixture.t, filepath.Join(externalTarget, "secret.txt"), externalSecretMarker+"\n")
	require.NoError(fixture.t, os.Symlink(externalTarget, filepath.Join(personalSkills, "external")),
		"external skills symlink must be created")

	otherCodexHome := filepath.Join(fixture.project.root, "other codex home")
	require.NoError(fixture.t, os.MkdirAll(otherCodexHome, 0o755), "alternate Codex home must be created")

	return codexSentinel{
		otherCodexHome: otherCodexHome,
		inspectReport:  filepath.Join(fixture.project.worktree, "codex-inspect.report"),
		personalSkills: personalSkills,
		externalTarget: externalTarget,
		fakeBinary:     fakeBinary,
	}
}

// assertCodexHomeMounts inspects the container view through the probe: personal skills are readable
// but not writable, the external symlink target is unavailable, and the Codex home is mounted with
// the fake binary present as inert data beside the image-owned Codex.
func (fixture *smokeFixture) assertCodexHomeMounts(sentinel codexSentinel) {
	fixture.t.Helper()
	probe := fixture.launcher.start(fixture.project.worktree, "bash", "-c", codexInspectScript, "bash", sentinel.inspectReport)
	probe.requireExit(fixture.t, "codex container-view inspection")
	report := parseReport(fixture.t, sentinel.inspectReport)

	require.Equal(fixture.t, "true", report["skill_readable"], "personal skill must be readable in the container")
	require.Equal(fixture.t, "false", report["skill_writable"], "personal skills must be mounted read-only")
	require.Equal(fixture.t, "false", report["external_available"], "external skills symlink target must be unavailable")
	require.Equal(fixture.t, "true", report["image_codex"], "image-owned Codex must exist at the absolute path")
	require.Equal(fixture.t, "true", report["fake_codex_present"], "host Codex binary must be visible as data under the state")
	require.Equal(fixture.t, "/usr/local/bin/codex", report["codex_on_path"],
		"a bare codex must resolve to the image binary, not the host binary under the mounted state")
	require.Equal(fixture.t, agentsMarker, report["agents_marker"], "mounted global instructions must be readable")
	require.Equal(fixture.t, codexSkillMarker, report["codex_skill_marker"], "mounted Codex skill must be readable")
	require.Equal(fixture.t, fixture.project.codexHome, report["codex_home_env"],
		"CODEX_HOME must point at the container Codex home")
}

// assertCodexReadsSentinelAndWritesState launches the product Codex, which reads the mounted model
// from config.toml and writes session state back to the host with host-editable ownership. Codex
// exits nonzero without credentials, which is sufficient to observe the read and the state write.
func (fixture *smokeFixture) assertCodexReadsSentinelAndWritesState() {
	fixture.t.Helper()
	codex := fixture.launcher.startBinary(
		fixture.launcher.productBinary,
		fixture.project.worktree,
		nil,
		"exec", "--skip-git-repo-check", "codex-safe sentinel probe",
	)
	codex.waitDone(fixture.t, "product codex exec")
	require.NotZero(fixture.t, codex.exitCode(), "codex exec must fail without credentials\n%s", codex.diagnostics())
	require.Contains(fixture.t, codex.stdout.String()+codex.stderr.String(), sentinelModel,
		"codex must resolve the model from the mounted config.toml")

	sessions := filepath.Join(fixture.project.codexHome, "sessions")
	require.DirExists(fixture.t, sessions, "codex must write session state under the mounted Codex home")
	require.NotEmpty(fixture.t, listFiles(fixture.t, sessions), "codex session state must contain a rollout file")
	requireHostOwnership(fixture.t, sessions)
}

// assertImageCodexNotShadowed runs the product doctor and proves the running executable is the
// image-owned path even though a host Codex binary sits under the mounted Codex home.
func (fixture *smokeFixture) assertImageCodexNotShadowed(sentinel codexSentinel) {
	fixture.t.Helper()
	require.FileExists(fixture.t, sentinel.fakeBinary, "the host Codex binary under the state must still exist")
	doctor := fixture.launcher.startBinary(fixture.launcher.productBinary, fixture.project.worktree, nil, "doctor")
	doctor.waitDone(fixture.t, "product codex doctor")
	combined := doctor.stdout.String() + doctor.stderr.String()
	require.Contains(fixture.t, combined, "/usr/local/bin/codex",
		"doctor must report the image-owned executable\n%s", doctor.diagnostics())
	require.NotContains(fixture.t, combined, "FAKE_CODEX_SHADOW", "the host Codex binary must never execute")
}

// assertReuseMismatchDiagnostic relaunches the same worktree with a different Codex home and proves
// the launcher reports the finish-active-session diagnostic without reusing or terminating the live
// container.
func (fixture *smokeFixture) assertReuseMismatchDiagnostic(sentinel codexSentinel, hold *launcherProcess) {
	fixture.t.Helper()
	require.True(fixture.t, hold.running(), "the held session must still be running before the mismatch launch")
	mismatch := fixture.launcher.startBinary(
		fixture.launcher.productBinary,
		fixture.project.worktree,
		[]string{"CODEX_HOME=" + sentinel.otherCodexHome},
		"doctor",
	)
	mismatch.waitDone(fixture.t, "codex reuse-mismatch launch")
	require.NotZero(fixture.t, mismatch.exitCode(), "a user-state mismatch must fail the launch")
	require.Contains(fixture.t, mismatch.stderr.String(), "finish the active session",
		"the launcher must report the finish-active-session diagnostic\n%s", mismatch.diagnostics())
	require.True(fixture.t, hold.running(), "the live session must not be terminated by a mismatched launch")
	require.True(fixture.t, fixture.docker.inspectOuter().State.Running, "the outer container must remain running")
	require.Len(fixture.t, fixture.docker.managedContainers(), 1, "a mismatch must not create a second container")
}

// TestSysboxCodexCredentialedAcceptance is an opt-in acceptance test for a full Codex turn. It runs
// only with a dedicated test account: set CODEX_SAFE_CREDENTIALED_ACCEPTANCE=1 and point
// CODEX_SAFE_TEST_AUTH_JSON at an auth.json for that account. Real credentials stay out of fixtures,
// logs, and CI artifacts; the file is copied into an ephemeral sentinel home and never printed.
func TestSysboxCodexCredentialedAcceptance(t *testing.T) {
	if os.Getenv(goSmokeEnv) != "1" || os.Getenv(credentialedEnv) != "1" {
		t.Skipf("set %s=1 and %s=1 with %s to run the credentialed acceptance test",
			goSmokeEnv, credentialedEnv, credentialSourceEnv)
	}
	authSource := os.Getenv(credentialSourceEnv)
	if authSource == "" {
		t.Fatalf("%s=1 requires %s to point at a dedicated test account auth.json", credentialedEnv, credentialSourceEnv)
	}
	fixture := newSmokeFixture(t)
	if _, err := os.Stat(fixture.launcher.productBinary); err != nil {
		t.Skip("bin/codex-safe is missing; run make build first")
	}
	fixture.setupCodexSentinel()
	authBytes, err := os.ReadFile(authSource)
	require.NoError(t, err, "test account auth.json must be readable")
	require.NoError(t, os.WriteFile(filepath.Join(fixture.project.codexHome, "auth.json"), authBytes, 0o600),
		"test credentials must install into the ephemeral sentinel Codex home")

	codex := fixture.launcher.startBinary(
		fixture.launcher.productBinary,
		fixture.project.worktree,
		nil,
		"exec", "--skip-git-repo-check", "Reply with the single word ACK and nothing else.",
	)
	codex.waitDone(t, "credentialed codex exec")
	require.Zero(t, codex.exitCode(), "a credentialed Codex turn must succeed")
	require.DirExists(t, filepath.Join(fixture.project.codexHome, "sessions"), "a completed turn must persist session state")
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755), "parent directory for %q must be created", path)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644), "file %q must be written", path)
}

func listFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !info.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	require.NoError(t, err, "walking %q must succeed", root)
	return files
}

func (process *launcherProcess) waitDone(t *testing.T, description string) {
	t.Helper()
	select {
	case <-process.done:
	case <-time.After(commandTimeout):
		t.Fatalf("timed out waiting for %s\n%s", description, process.diagnostics())
	}
}

func (process *launcherProcess) exitCode() int {
	if process.err == nil {
		return 0
	}
	var exitError *exec.ExitError
	if errors.As(process.err, &exitError) {
		return exitError.ExitCode()
	}
	return -1
}

// codexInspectScript observes the container's view of the mounted user state and reports facts for
// Go assertions. It avoids set -e so individual negative probes do not abort the report.
const codexInspectScript = `set -uo pipefail
report=$1
skill_dir="$HOME/.agents/skills/personal-sentinel"
skill_readable=false
[[ -r "$skill_dir/SKILL.md" ]] && grep -q ` + personalSkillMarker + ` "$skill_dir/SKILL.md" && skill_readable=true
skill_writable=true
( : > "$skill_dir/intrusion" ) 2>/dev/null || skill_writable=false
rm -f "$skill_dir/intrusion" 2>/dev/null
external_available=true
cat "$HOME/.agents/skills/external/secret.txt" >/dev/null 2>&1 || external_available=false
image_codex=false
[[ -x /usr/local/bin/codex ]] && image_codex=true
fake_codex_present=false
[[ -e "$CODEX_HOME/bin/codex" ]] && fake_codex_present=true
codex_on_path="$(command -v codex || true)"
agents_marker="$(cat "$CODEX_HOME/AGENTS.md" 2>/dev/null | tr -d '\n')"
codex_skill_marker="$(cat "$CODEX_HOME/skills/sentinel-skill/SKILL.md" 2>/dev/null | grep ` + codexSkillMarker + `)"
printf 'skill_readable=%s\nskill_writable=%s\nexternal_available=%s\nimage_codex=%s\nfake_codex_present=%s\ncodex_on_path=%s\nagents_marker=%s\ncodex_skill_marker=%s\ncodex_home_env=%s\n' \
    "$skill_readable" "$skill_writable" "$external_available" "$image_codex" "$fake_codex_present" \
    "$codex_on_path" "$agents_marker" "$codex_skill_marker" "$CODEX_HOME" > "$report"`
