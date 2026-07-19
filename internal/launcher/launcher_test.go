package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/dockercli"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/hostmcp"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
	"github.com/vkuptcov/agents-safe-environment/internal/session"
)

func TestDockerLaunchCreatesDetachedContainerThenExecutesResolvedPlan(t *testing.T) {
	containerID := strings.Repeat("a", 64)
	runner := &fakeCommandRunner{outputs: []commandResult{
		containerNotFound(),
		{output: []byte(`{"runc":{},"sysbox-runc":{}}`)},
		{output: []byte(`[]`)},
		{output: []byte(containerID + "\n")},
	}}
	docker := testDocker(runner)
	plan := simplePlan()
	if err := docker.Launch(context.Background(), plan, "image", []string{"echo", "safe"}, launchplan.Options{}); err != nil {
		t.Fatalf("Launch() error = %v", err)
	}

	containerName := mustContainerName(t, docker.HostUID, plan.ProjectRoot)
	wantCalls := [][]string{
		{"docker", "container", "inspect", containerName},
		{"docker", "info", "--format", "{{json .Runtimes}}"},
		{"docker", "image", "inspect", "image"},
	}
	if !reflect.DeepEqual(runner.combinedCalls[:3], wantCalls) {
		t.Fatalf("pre-create calls = %#v, want %#v", runner.combinedCalls[:3], wantCalls)
	}
	assertWrappedRun(t, runner.runCalls, containerID, []string{"echo", "safe"})
}

func TestDockerLaunchReusesExactRunningContainer(t *testing.T) {
	containerID := strings.Repeat("b", 64)
	plan := simplePlan()
	runner := &fakeCommandRunner{outputs: []commandResult{{
		output: inspectionJSON(t, containerID, true, "running", matchingLabels(t, plan, 1000)),
	}}}
	docker := testDocker(runner)
	docker.AllocateTTY = true
	if err := docker.Launch(context.Background(), plan, "image", []string{"make", "test"}, launchplan.Options{}); err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	if len(runner.combinedCalls) != 1 {
		t.Fatalf("CombinedOutput calls = %#v, want exact inspect only", runner.combinedCalls)
	}
	assertWrappedRun(t, runner.runCalls, containerID, []string{"make", "test"})
	if !containsSequence(runner.runCalls[0], "exec", "--interactive", "--tty") {
		t.Fatalf("exec does not preserve TTY: %#v", runner.runCalls[0])
	}
}

func TestDockerLaunchIgnoresDiagnosticMountLabelsWhenFingerprintMatches(t *testing.T) {
	plan := simplePlan()
	labels := matchingLabels(t, plan, 1000)
	labels[codexHomeLabel] = "/home/developer/other-codex"
	runner := &fakeCommandRunner{outputs: []commandResult{{
		output: inspectionJSON(t, strings.Repeat("7", 64), true, "running", labels),
	}}}
	if err := testDocker(runner).Launch(context.Background(), plan, "image", []string{"true"}, launchplan.Options{}); err != nil {
		t.Fatalf("Launch() error = %v, want fingerprint-controlled reuse", err)
	}
	if len(runner.runCalls) != 1 {
		t.Fatalf("Run calls = %#v, want reuse", runner.runCalls)
	}
}

func TestDockerLaunchRejectsMismatchedDeterministicNameOccupant(t *testing.T) {
	plan := simplePlan()
	labels := matchingLabels(t, plan, 1000)
	labels[projectPathLabel] = "/other-project"
	runner := &fakeCommandRunner{outputs: []commandResult{{
		output: inspectionJSON(t, strings.Repeat("c", 64), true, "running", labels),
	}}}
	err := testDocker(runner).Launch(context.Background(), plan, "image", []string{"true"}, launchplan.Options{})
	if err == nil || !strings.Contains(err.Error(), "refusing deterministic-name reuse") {
		t.Fatalf("Launch() error = %v, want ownership rejection", err)
	}
}

func TestDockerLaunchExplicitImageBypassesInvalidProjectEnvironment(t *testing.T) {
	root, plan := invalidProjectPlan(t)
	_ = root
	containerID := strings.Repeat("d", 64)
	runner := &fakeCommandRunner{outputs: []commandResult{
		containerNotFound(),
		{output: []byte(`{"runc":{},"sysbox-runc":{}}`)},
		{output: []byte(`[]`)},
		{output: []byte(containerID + "\n")},
	}}
	if err := testDocker(runner).Launch(context.Background(), plan, "image", []string{"true"}, launchplan.Options{ImageOverride: true}); err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
}

func TestDockerLaunchRejectsInvalidProjectEnvironmentWithoutImageOverride(t *testing.T) {
	_, plan := invalidProjectPlan(t)
	runner := &fakeCommandRunner{outputs: []commandResult{containerNotFound()}}
	err := testDocker(runner).Launch(context.Background(), plan, "image", []string{"true"}, launchplan.Options{})
	if err == nil || !strings.Contains(err.Error(), "project Dockerfile") {
		t.Fatalf("Launch() error = %v, want project Dockerfile validation failure", err)
	}
	if len(runner.combinedCalls) != 1 || len(runner.runCalls) != 0 {
		t.Fatalf("invalid context calls = combined %#v run %#v", runner.combinedCalls, runner.runCalls)
	}
}

func TestDockerLaunchBuildsProjectImageAndPinsCreate(t *testing.T) {
	root, contextPath := writeProjectDefinition(t)
	derivedID := "sha256:" + strings.Repeat("b", 64)
	containerID := strings.Repeat("c", 64)
	runner := &fakeCommandRunner{outputs: []commandResult{
		containerNotFound(),
		{output: []byte(`{"runc":{},"sysbox-runc":{}}`)},
		{output: []byte(`[]`)},
		{output: imageInspectionJSON(t, derivedID)},
		{output: []byte(containerID + "\n")},
	}}
	plan := projectOnlyPlan(root)
	docker := testDocker(runner)
	if err := docker.Launch(context.Background(), plan, "base:image", []string{"true"}, launchplan.Options{}); err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	if len(runner.runCalls) != 2 {
		t.Fatalf("Run calls = %#v, want build and exec", runner.runCalls)
	}
	build := runner.runCalls[0]
	if !containsSequence(build, "build", "--tag") || build[len(build)-1] != contextPath {
		t.Fatalf("project build call = %#v", build)
	}
	create := runner.combinedCalls[len(runner.combinedCalls)-1]
	if create[len(create)-1] != derivedID {
		t.Fatalf("create image = %q, want immutable derived ID %q", create[len(create)-1], derivedID)
	}
}

func TestDockerLaunchRejectsMissingSysbox(t *testing.T) {
	runner := &fakeCommandRunner{outputs: []commandResult{
		containerNotFound(),
		{output: []byte(`{"runc":{}}`)},
	}}
	err := testDocker(runner).Launch(context.Background(), simplePlan(), "image", []string{"true"}, launchplan.Options{ImageOverride: true})
	if err == nil || !strings.Contains(err.Error(), "sysbox-runc") {
		t.Fatalf("Launch() error = %v, want missing Sysbox runtime", err)
	}
}

func TestValidateConfigurationRejectsInvalidIdentityInputs(t *testing.T) {
	tests := []struct {
		name      string
		hostUser  string
		hostGroup string
		hostHome  string
		want      string
	}{
		{name: "empty user", hostGroup: "developers", hostHome: "/home/developer", want: "host user name is empty"},
		{name: "unsafe group", hostUser: "developer", hostGroup: "bad group", hostHome: "/home/developer", want: "host group name"},
		{name: "relative home", hostUser: "developer", hostGroup: "developers", hostHome: "relative/home", want: "host home directory"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			docker := &DockerLauncher{
				DockerBinary: "docker", CommandRunner: &fakeCommandRunner{}, HostOS: "linux",
				HostUID: 1000, HostGID: 1000, HostUser: test.hostUser, HostGroup: test.hostGroup, HostHome: test.hostHome,
			}
			if err := docker.validateConfiguration(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateConfiguration() error = %v, want %q", err, test.want)
			}
		})
	}
}

func testPlan() launchplan.Plan {
	return planWithCodex("/sources/feature worktree", "/sources/feature worktree/nested", "/home/developer/.codex")
}

func simplePlan() launchplan.Plan {
	return planWithCodex("/project", "/project/nested", "/home/developer/.codex")
}

func projectOnlyPlan(root string) launchplan.Plan {
	worktree := launchplan.BindMount{Source: root, Target: root}
	return launchplan.Plan{
		ProjectRoot: root,
		WorkingDir:  root,
		Mounts:      []launchplan.BindMount{worktree},
		Provenance:  []launchplan.MountProvenance{{Mount: worktree, Roles: []projectenv.MountRole{projectenv.RoleWorktree}}},
		Roles:       []projectenv.MountRole{projectenv.RoleWorktree},
	}
}

func planWithCodex(root, workingDir, codexHome string) launchplan.Plan {
	primary := launchplan.BindMount{Source: root + "/primary", Target: root + "/primary", ReadOnly: true}
	commonGit := launchplan.BindMount{Source: root + "/common.git", Target: root + "/common.git"}
	worktree := launchplan.BindMount{Source: root, Target: root}
	codex := launchplan.BindMount{Source: codexHome, Target: "/home/developer/.codex"}
	return launchplan.Plan{
		ProjectRoot: root,
		WorkingDir:  workingDir,
		Mounts:      []launchplan.BindMount{primary, commonGit, worktree, codex},
		Provenance: []launchplan.MountProvenance{
			{Mount: primary, Roles: []projectenv.MountRole{projectenv.RolePrimaryCheckout}},
			{Mount: commonGit, Roles: []projectenv.MountRole{projectenv.RoleCommonGitDir}},
			{Mount: worktree, Roles: []projectenv.MountRole{projectenv.RoleWorktree}},
			{Mount: codex, Roles: []projectenv.MountRole{projectenv.RoleCodexHome}},
		},
		Roles: []projectenv.MountRole{
			projectenv.RolePrimaryCheckout,
			projectenv.RoleCommonGitDir,
			projectenv.RoleWorktree,
			projectenv.RoleCodexHome,
		},
	}
}

func invalidProjectPlan(t *testing.T) (string, launchplan.Plan) {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, projectenv.Directory), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", filepath.Join(root, projectenv.Directory, projectenv.DockerfileName)); err != nil {
		t.Fatal(err)
	}
	return root, projectOnlyPlan(root)
}

func testDocker(runner dockercli.Runner) *DockerLauncher {
	return &DockerLauncher{
		DockerBinary: "docker", CommandRunner: runner, HostOS: "linux", Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard,
		HostUID: 1000, HostGID: 1000, HostUser: "developer", HostGroup: "developers", HostHome: "/home/developer",
		LookupEnv: func(string) (string, bool) { return "", false },
	}
}

// hostLauncher supplies only the identity request builders need. The fourth argument is retained
// at the test boundary while host Git is now a resolved plan role rather than launcher state.
func hostLauncher(hostUID, hostGID int, hostHome, _ string) *DockerLauncher {
	return &DockerLauncher{HostUID: hostUID, HostGID: hostGID, HostUser: "developer", HostGroup: "developers", HostHome: hostHome}
}

func mustContainerName(t *testing.T, hostUID int, projectRoot string) string {
	t.Helper()
	return ProjectContainerName(hostUID, projectRoot)
}

func matchingLabels(t *testing.T, plan launchplan.Plan, hostUID int) map[string]string {
	t.Helper()
	fingerprint, err := creationFingerprint(plan, "image", false, false, hostmcp.Set{})
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		managedLabel: managedLabelValue, projectPathLabel: plan.ProjectRoot, hostUIDLabel: strconv.Itoa(hostUID),
		managerProtocolLabel: session.ProtocolVersion, codexHomeLabel: mountRoleLabel(plan, projectenv.RoleCodexHome),
		personalSkillsLabel: mountRoleLabel(plan, projectenv.RolePersonalSkills), hostMCPLabel: "absent",
		launchConfigLabel: fingerprint,
	}
}

func assertLabel(t *testing.T, args []string, name, want string) {
	t.Helper()
	target := name + "=" + want
	for index, arg := range args {
		if arg == "--label" && index+1 < len(args) && args[index+1] == target {
			return
		}
	}
	t.Fatalf("args missing --label %q: %#v", target, args)
}

func inspectionJSON(t *testing.T, containerID string, running bool, status string, labels map[string]string) []byte {
	t.Helper()
	inspection := dockercli.ContainerInspection{ID: containerID}
	inspection.Config.Labels = labels
	inspection.State.Running = running
	inspection.State.Status = status
	data, err := json.Marshal([]dockercli.ContainerInspection{inspection})
	if err != nil {
		t.Fatalf("marshal inspection: %v", err)
	}
	return data
}

func imageInspectionJSON(t *testing.T, imageID string) []byte {
	t.Helper()
	inspection := dockercli.ImageInspection{ID: imageID, Architecture: runtime.GOARCH}
	inspection.Config.Entrypoint = []string{"/usr/bin/tini", "--", "/usr/local/bin/codex-safe-session"}
	inspection.Config.Command = []string{"serve"}
	inspection.Config.Environment = []string{"DOCKER_HOST=unix:///var/run/docker.sock"}
	data, err := json.Marshal([]dockercli.ImageInspection{inspection})
	if err != nil {
		t.Fatalf("marshal image inspection: %v", err)
	}
	return data
}

func writeProjectDefinition(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	contextPath := filepath.Join(root, projectenv.Directory)
	if err := os.Mkdir(contextPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contextPath, projectenv.DockerfileName), []byte("ARG AGENTS_SAFE_BASE\nFROM ${AGENTS_SAFE_BASE}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	discovered, err := projectenv.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	return root, discovered
}

func containerNotFound() commandResult {
	return commandResult{output: []byte("Error: No such container: codex-safe-test"), err: fakeExitError{code: 1}}
}

func containsSequence(values []string, sequence ...string) bool {
	for index := range values {
		if index+len(sequence) <= len(values) && reflect.DeepEqual(values[index:index+len(sequence)], sequence) {
			return true
		}
	}
	return false
}

func assertWrappedRun(t *testing.T, calls [][]string, containerID string, command []string) {
	t.Helper()
	if len(calls) != 1 {
		t.Fatalf("Run calls = %#v, want one", calls)
	}
	wantSuffix := append([]string{containerID, "codex-safe-session", "run", "--"}, command...)
	if len(calls[0]) < len(wantSuffix) || !reflect.DeepEqual(calls[0][len(calls[0])-len(wantSuffix):], wantSuffix) {
		t.Fatalf("wrapped exec = %#v, want suffix %#v", calls[0], wantSuffix)
	}
}

type commandResult struct {
	output []byte
	err    error
}

type fakeCommandRunner struct {
	outputs       []commandResult
	combinedCalls [][]string
	runCalls      [][]string
	runErrors     []error
}

func (runner *fakeCommandRunner) CombinedOutput(_ context.Context, name string, arguments ...string) ([]byte, error) {
	runner.combinedCalls = append(runner.combinedCalls, append([]string{name}, arguments...))
	if len(runner.outputs) == 0 {
		return nil, errors.New("unexpected CombinedOutput call")
	}
	result := runner.outputs[0]
	runner.outputs = runner.outputs[1:]
	return result.output, result.err
}

func (runner *fakeCommandRunner) Run(_ context.Context, name string, arguments []string, _ io.Reader, _ io.Writer, _ io.Writer) error {
	runner.runCalls = append(runner.runCalls, append([]string{name}, arguments...))
	if len(runner.runErrors) == 0 {
		return nil
	}
	err := runner.runErrors[0]
	runner.runErrors = runner.runErrors[1:]
	return err
}

type fakeExitError struct{ code int }

func (err fakeExitError) Error() string { return "exit failure" }
func (err fakeExitError) ExitCode() int { return err.code }
