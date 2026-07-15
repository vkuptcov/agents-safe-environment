package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/terminal"
)

func TestBuildDockerRunArgsUsesDetachedSysboxAndIdentityLabels(t *testing.T) {
	t.Parallel()
	plan := testPlan()
	args, err := BuildDockerRunArgs(
		plan,
		"codex-safe-mvp:local",
		"codex-safe-aba8b4ca4ff345d5d0443c0c",
		1000,
		1001,
		"developer",
		"developers",
		"/home/developer",
		"",
		testUserState(),
	)
	if err != nil {
		t.Fatalf("BuildDockerRunArgs() error = %v", err)
	}
	wantPrefix := []string{
		"run",
		"--detach",
		"--rm",
		"--runtime=sysbox-runc",
		"--name",
		"codex-safe-aba8b4ca4ff345d5d0443c0c",
		"--label",
		"codex-safe.managed=true",
		"--label",
		"codex-safe.project-path=/sources/feature worktree",
		"--label",
		"codex-safe.host-uid=1000",
		"--label",
		"codex-safe.manager-protocol=1",
		"--label",
		"codex-safe.codex-home=/home/developer/.codex",
		"--label",
		"codex-safe.personal-skills=absent",
		"--env",
		"CODEX_SAFE_HOST_UID=1000",
		"--env",
		"CODEX_SAFE_HOST_GID=1001",
		"--env",
		"CODEX_SAFE_HOST_USER=developer",
		"--env",
		"CODEX_SAFE_HOST_GROUP=developers",
		"--env",
		"CODEX_SAFE_HOST_HOME=/home/developer",
		"--workdir",
		plan.WorkingDir,
	}
	if !reflect.DeepEqual(args[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("args prefix = %#v, want %#v", args[:len(wantPrefix)], wantPrefix)
	}
	if got := args[len(args)-1]; got != "codex-safe-mvp:local" {
		t.Fatalf("last argument = %q, want image", got)
	}
	joined := strings.Join(args, " ")
	for _, forbidden := range []string{
		"--interactive",
		"--tty",
		"--privileged",
		"--network=host",
		"--pid=host",
		"/var/run/docker.sock",
		"codex-safe.session",
	} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("docker run args contain forbidden value %q: %s", forbidden, joined)
		}
	}
}

func TestBuildDockerRunArgsPreservesMountOrderAndUserState(t *testing.T) {
	t.Parallel()
	// A custom CODEX_HOME source differs from its container target, and skills are present, so this
	// exercises every mount mode and the source-not-equal-target user-state mounts in one place.
	userState := UserState{CodexHome: "/host/custom codex", PersonalSkills: "/host/skills"}
	args, err := BuildDockerRunArgs(
		testPlan(),
		"image",
		"codex-safe-test",
		1000,
		1000,
		"developer",
		"developers",
		"/home/developer profile",
		"/home/developer profile/.gitconfig",
		userState,
	)
	if err != nil {
		t.Fatalf("BuildDockerRunArgs() error = %v", err)
	}
	var mounts []string
	for index, argument := range args {
		if argument == "--mount" {
			mounts = append(mounts, args[index+1])
		}
	}
	want := []string{
		"type=bind,source=/home/developer profile/.gitconfig," +
			"target=/home/developer profile/.gitconfig,bind-propagation=rprivate,readonly",
		"type=bind,source=/sources/primary,target=/sources/primary,bind-propagation=rprivate,readonly",
		"type=bind,source=/sources/primary/.git,target=/sources/primary/.git,bind-propagation=rprivate",
		"type=bind,source=/sources/feature worktree," +
			"target=/sources/feature worktree,bind-propagation=rprivate",
		"type=bind,source=/host/custom codex," +
			"target=/home/developer profile/.codex,bind-propagation=rprivate",
		"type=bind,source=/host/skills," +
			"target=/home/developer profile/.agents/skills,bind-propagation=rprivate,readonly",
	}
	if !reflect.DeepEqual(mounts, want) {
		t.Fatalf("mounts = %#v, want %#v", mounts, want)
	}
}

func TestBuildDockerRunArgsRejectsMissingCodexHome(t *testing.T) {
	t.Parallel()
	_, err := BuildDockerRunArgs(
		testPlan(), "image", "codex-safe-test", 1000, 1000,
		"developer", "developers", "/home/developer", "",
		UserState{PersonalSkills: PersonalSkillsAbsent},
	)
	if err == nil || !strings.Contains(err.Error(), "resolved Codex home is required") {
		t.Fatalf("BuildDockerRunArgs() error = %v, want missing Codex home", err)
	}
}

func TestBuildDockerRunArgsOmitsAbsentCodexHome(t *testing.T) {
	t.Parallel()
	// agents-safe with no host Codex home: the container is created with no Codex mount and the
	// codex-home label records the absent marker so reuse still matches.
	userState := UserState{CodexHome: CodexHomeAbsent, PersonalSkills: PersonalSkillsAbsent}
	args, err := BuildDockerRunArgs(
		testPlan(), "image", "codex-safe-test", 1000, 1000,
		"developer", "developers", "/home/developer profile", "",
		userState,
	)
	if err != nil {
		t.Fatalf("BuildDockerRunArgs() error = %v", err)
	}
	assertLabel(t, args, codexHomeLabel, CodexHomeAbsent)
	for index, argument := range args {
		if argument == "--mount" && strings.Contains(args[index+1], "/.codex") {
			t.Errorf("run args mount a Codex home when absent: %q", args[index+1])
		}
	}
}

func TestBuildDockerExecArgsOmitsCodexHomeWhenAbsent(t *testing.T) {
	t.Parallel()
	args, err := BuildDockerExecArgs(
		testPlan(), []string{"bash"}, strings.Repeat("a", 64), 1000, 1001, "/home/developer profile", false, false,
	)
	if err != nil {
		t.Fatalf("BuildDockerExecArgs() error = %v", err)
	}
	for _, argument := range args {
		if strings.HasPrefix(argument, "CODEX_HOME=") {
			t.Errorf("exec args set CODEX_HOME when no Codex home is mounted: %#v", args)
		}
	}
}

func TestBuildDockerExecArgsWrapsCommandAndPreservesTerminalContract(t *testing.T) {
	t.Parallel()
	containerID := strings.Repeat("a", 64)
	command := []string{"printf", "%s\\n", "value with spaces; $(not-a-shell)", ""}
	args, err := BuildDockerExecArgs(testPlan(), command, containerID, 1000, 1001, "/home/developer profile", true, true)
	if err != nil {
		t.Fatalf("BuildDockerExecArgs() error = %v", err)
	}
	want := []string{
		"exec",
		"--interactive",
		"--tty",
		"--user",
		"1000:1001",
		"--env",
		"HOME=/home/developer profile",
		"--env",
		"CODEX_HOME=/home/developer profile/.codex",
		"--workdir",
		"/sources/feature worktree/nested",
		containerID,
		"codex-safe-session",
		"run",
		"--",
		"printf",
		"%s\\n",
		"value with spaces; $(not-a-shell)",
		"",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("BuildDockerExecArgs() = %#v, want %#v", args, want)
	}
}

func TestBuildDockerRunArgsRejectsInvalidIdentityInputs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		hostUser  string
		hostGroup string
		hostHome  string
		gitConfig string
		want      string
	}{
		{name: "empty user", hostGroup: "developers", hostHome: "/home/developer", want: "host user name is empty"},
		{
			name: "unsafe user", hostUser: "bad:user", hostGroup: "developers", hostHome: "/home/developer",
			want: "host user name",
		},
		{
			name: "unsafe group", hostUser: "developer", hostGroup: "bad group", hostHome: "/home/developer",
			want: "host group name",
		},
		{
			name: "relative home", hostUser: "developer", hostGroup: "developers", hostHome: "relative/home",
			want: "host home directory",
		},
		{
			name: "relative Git config", hostUser: "developer", hostGroup: "developers",
			hostHome: "/home/developer", gitConfig: "relative/.gitconfig", want: "host Git config",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := BuildDockerRunArgs(
				testPlan(), "image", "codex-safe-test", 1000, 1000,
				test.hostUser, test.hostGroup, test.hostHome, test.gitConfig, testUserState(),
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("BuildDockerRunArgs() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestDockerLaunchCreatesDetachedContainerThenExecutesWrapper(t *testing.T) {
	t.Parallel()
	containerID := strings.Repeat("a", 64)
	runner := &fakeCommandRunner{outputs: []commandResult{
		containerNotFound(),
		{output: []byte(`{"runc":{},"sysbox-runc":{}}`)},
		{output: []byte(`[]`)},
		{output: []byte(containerID + "\n")},
	}}
	docker := testDocker(runner)
	if err := docker.Launch(context.Background(), simplePlan(), "image", []string{"echo", "safe"}); err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	containerName := mustContainerName(t, docker.HostUID, "/project")
	wantCalls := [][]string{
		{"docker", "container", "inspect", containerName},
		{"docker", "info", "--format", "{{json .Runtimes}}"},
		{"docker", "image", "inspect", "image"},
	}
	if !reflect.DeepEqual(runner.combinedCalls[:3], wantCalls) {
		t.Fatalf("pre-create calls = %#v, want %#v", runner.combinedCalls[:3], wantCalls)
	}
	create := runner.combinedCalls[3]
	if !containsSequence(create, "run", "--detach", "--rm") || !containsSequence(create, "--name", containerName) {
		t.Fatalf("create call = %#v", create)
	}
	assertWrappedRun(t, runner.runCalls, containerID, []string{"echo", "safe"})
}

func TestDockerLaunchReusesExactRunningContainer(t *testing.T) {
	t.Parallel()
	containerID := strings.Repeat("b", 64)
	runner := &fakeCommandRunner{outputs: []commandResult{{
		output: inspectionJSON(t, containerID, true, "running", matchingLabels("/project", 1000)),
	}}}
	docker := testDocker(runner)
	docker.TTY = true
	if err := docker.Launch(context.Background(), simplePlan(), "image", []string{"make", "test"}); err != nil {
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

func TestDockerLaunchRejectsMismatchedDeterministicNameOccupant(t *testing.T) {
	t.Parallel()
	containerID := strings.Repeat("c", 64)
	labels := matchingLabels("/other-project", 1000)
	runner := &fakeCommandRunner{outputs: []commandResult{{
		output: inspectionJSON(t, containerID, true, "running", labels),
	}}}
	err := testDocker(runner).Launch(context.Background(), simplePlan(), "image", []string{"true"})
	if err == nil || !strings.Contains(err.Error(), "refusing deterministic-name reuse") {
		t.Fatalf("Launch() error = %v, want label mismatch", err)
	}
	if len(runner.runCalls) != 0 {
		t.Fatalf("Run calls = %#v, want none", runner.runCalls)
	}
}

func TestDockerLaunchRejectsUserStateMismatchWithActiveSessionDiagnostic(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		label      string
		runningVal string
	}{
		{name: "codex home", label: codexHomeLabel, runningVal: "/home/developer/other-codex"},
		{name: "personal skills", label: personalSkillsLabel, runningVal: "/host/other-skills"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			containerID := strings.Repeat("7", 64)
			labels := matchingLabels("/project", 1000)
			labels[test.label] = test.runningVal
			runner := &fakeCommandRunner{outputs: []commandResult{{
				output: inspectionJSON(t, containerID, true, "running", labels),
			}}}
			err := testDocker(runner).Launch(context.Background(), simplePlan(), "image", []string{"true"})
			if err == nil || !strings.Contains(err.Error(), "finish the active session") {
				t.Fatalf("Launch() error = %v, want finish-active-session diagnostic", err)
			}
			if !strings.Contains(err.Error(), test.label) {
				t.Fatalf("diagnostic must name %s: %v", test.label, err)
			}
			var mismatch *userStateMismatchError
			if !errors.As(err, &mismatch) {
				t.Fatalf("error must be a userStateMismatchError: %v", err)
			}
			// Neither reuse (no exec) nor terminate (no stop/rm): only the single inspect ran.
			if len(runner.combinedCalls) != 1 {
				t.Fatalf("CombinedOutput calls = %#v, want inspect only", runner.combinedCalls)
			}
			if len(runner.runCalls) != 0 {
				t.Fatalf("Run calls = %#v, want none", runner.runCalls)
			}
		})
	}
}

func TestDockerLaunchWaitsOutStoppedUserStateMismatch(t *testing.T) {
	t.Parallel()
	// A stopped container with matching ownership but a different Codex home is not an active
	// session: the launcher must wait for the deterministic name to release and create a fresh
	// container with the new user state, never report the finish-active-session diagnostic.
	oldID := strings.Repeat("8", 64)
	newID := strings.Repeat("9", 64)
	stopped := matchingLabels("/project", 1000)
	stopped[codexHomeLabel] = "/home/developer/other-codex"
	runner := &fakeCommandRunner{
		outputs: []commandResult{
			{output: inspectionJSON(t, oldID, false, "exited", stopped)},
			containerNotFound(),
			{output: []byte(`{"sysbox-runc":{}}`)},
			{output: []byte(`[]`)},
			{output: []byte(newID)},
		},
	}
	if err := testDocker(runner).Launch(context.Background(), simplePlan(), "image", []string{"true"}); err != nil {
		t.Fatalf("Launch() error = %v, want a fresh container for the stopped mismatch", err)
	}
	assertWrappedRun(t, runner.runCalls, newID, []string{"true"})
}

func TestWritableMountSourcesExcludesReadOnly(t *testing.T) {
	t.Parallel()
	got := writableMountSources([]Mount{
		{Source: "/sources/primary", Target: "/sources/primary", ReadOnly: true},
		{Source: "/sources/primary/.git", Target: "/sources/primary/.git"},
		{Source: "/sources/feature worktree", Target: "/sources/feature worktree"},
	})
	want := []string{"/sources/primary/.git", "/sources/feature worktree"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("writableMountSources() = %#v, want the read-write sources only %#v", got, want)
	}
}

func TestBuildDockerRunArgsRecordsUserStateLabels(t *testing.T) {
	t.Parallel()
	userState := UserState{CodexHome: "/host/custom codex", PersonalSkills: "/host/skills"}
	args, err := BuildDockerRunArgs(
		testPlan(), "image", "codex-safe-test", 1000, 1000,
		"developer", "developers", "/home/developer", "", userState,
	)
	if err != nil {
		t.Fatalf("BuildDockerRunArgs() error = %v", err)
	}
	assertLabel(t, args, codexHomeLabel, "/host/custom codex")
	assertLabel(t, args, personalSkillsLabel, "/host/skills")
}

func TestDockerLaunchReusesConcurrentCreateWinner(t *testing.T) {
	t.Parallel()
	containerID := strings.Repeat("d", 64)
	runner := &fakeCommandRunner{outputs: []commandResult{
		containerNotFound(),
		{output: []byte(`{"sysbox-runc":{}}`)},
		{output: []byte(`[]`)},
		{output: []byte("Conflict. The container name is already in use by container other"), err: fakeExitError{125}},
		{output: inspectionJSON(t, containerID, true, "running", matchingLabels("/project", 1000))},
	}}
	if err := testDocker(runner).Launch(context.Background(), simplePlan(), "image", []string{"true"}); err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	assertWrappedRun(t, runner.runCalls, containerID, []string{"true"})
}

func TestDockerLaunchWaitsForStoppedNameReleaseBeforeCreate(t *testing.T) {
	t.Parallel()
	oldID := strings.Repeat("e", 64)
	newID := strings.Repeat("f", 64)
	runner := &fakeCommandRunner{outputs: []commandResult{
		{output: inspectionJSON(t, oldID, false, "exited", matchingLabels("/project", 1000))},
		containerNotFound(),
		{output: []byte(`{"sysbox-runc":{}}`)},
		{output: []byte(`[]`)},
		{output: []byte(newID)},
	}}
	if err := testDocker(runner).Launch(context.Background(), simplePlan(), "image", []string{"true"}); err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	assertWrappedRun(t, runner.runCalls, newID, []string{"true"})
}

func TestDockerLaunchPreservesChildExitWhenContainerStillRuns(t *testing.T) {
	t.Parallel()
	containerID := strings.Repeat("1", 64)
	exitError := fakeExitError{37}
	runner := &fakeCommandRunner{
		outputs: []commandResult{
			{output: inspectionJSON(t, containerID, true, "running", matchingLabels("/project", 1000))},
			{output: inspectionJSON(t, containerID, true, "running", matchingLabels("/project", 1000))},
		},
		runErrors: []error{exitError},
	}
	err := testDocker(runner).Launch(context.Background(), simplePlan(), "image", []string{"false"})
	var got fakeExitError
	if !errors.As(err, &got) || got.ExitCode() != 37 {
		t.Fatalf("Launch() error = %v, want child exit 37", err)
	}
	if len(runner.runCalls) != 1 {
		t.Fatalf("Run calls = %d, want no retry", len(runner.runCalls))
	}
}

func TestDockerLaunchRetriesOnceAfterCommittedShutdown(t *testing.T) {
	t.Parallel()
	oldID := strings.Repeat("2", 64)
	newID := strings.Repeat("3", 64)
	runner := &fakeCommandRunner{
		outputs: []commandResult{
			{output: inspectionJSON(t, oldID, true, "running", matchingLabels("/project", 1000))},
			containerNotFound(),
			containerNotFound(),
			{output: []byte(`{"sysbox-runc":{}}`)},
			{output: []byte(`[]`)},
			{output: []byte(newID)},
		},
		runErrors: []error{
			&dockerCommandError{
				err:    fakeExitError{1},
				stderr: "Error response from daemon: container is not running",
			},
			nil,
		},
	}
	if err := testDocker(runner).Launch(context.Background(), simplePlan(), "image", []string{"true"}); err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	if len(runner.runCalls) != 2 {
		t.Fatalf("Run calls = %d, want one retry", len(runner.runCalls))
	}
	assertWrappedRun(t, runner.runCalls[1:], newID, []string{"true"})
}

func TestDockerLaunchDoesNotRetryUserCommandExit125(t *testing.T) {
	t.Parallel()
	containerID := strings.Repeat("4", 64)
	runner := &fakeCommandRunner{
		outputs: []commandResult{
			{output: inspectionJSON(t, containerID, true, "running", matchingLabels("/project", 1000))},
		},
		runErrors: []error{&dockerCommandError{
			err:    fakeExitError{125},
			stderr: "user command completed with status 125",
		}},
	}
	err := testDocker(runner).Launch(context.Background(), simplePlan(), "image", []string{"true"})
	if err == nil {
		t.Fatal("Launch() succeeded after user command exit 125")
	}
	if len(runner.runCalls) != 1 {
		t.Fatalf("Run calls = %d, want no retry for user status 125", len(runner.runCalls))
	}
}

func TestDockerLaunchRejectsMissingSysbox(t *testing.T) {
	t.Parallel()
	runner := &fakeCommandRunner{outputs: []commandResult{
		containerNotFound(),
		{output: []byte(`{"runc":{}}`)},
	}}
	err := testDocker(runner).Launch(context.Background(), simplePlan(), "image", []string{"true"})
	if err == nil || !strings.Contains(err.Error(), `Docker runtime "sysbox-runc" is not registered`) {
		t.Fatalf("Launch() error = %v", err)
	}
	if len(runner.runCalls) != 0 {
		t.Fatalf("Run calls = %#v", runner.runCalls)
	}
}

func TestDockerLaunchRejectsUnsupportedOSBeforeDocker(t *testing.T) {
	t.Parallel()
	runner := &fakeCommandRunner{}
	docker := testDocker(runner)
	docker.GOOS = "darwin"
	err := docker.Launch(context.Background(), simplePlan(), "image", []string{"true"})
	if err == nil || !strings.Contains(err.Error(), "requires Linux") {
		t.Fatalf("Launch() error = %v", err)
	}
	if len(runner.combinedCalls) != 0 || len(runner.runCalls) != 0 {
		t.Fatal("unsupported OS reached Docker")
	}
}

func TestDiscoverHostGitConfig(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	got, err := discoverHostGitConfig(home)
	if err != nil || got != "" {
		t.Fatalf("missing config = %q, error=%v", got, err)
	}
	path := filepath.Join(home, ".gitconfig")
	if err := os.WriteFile(path, []byte("[user]\n\tname = Developer\n"), 0o600); err != nil {
		t.Fatalf("write Git config: %v", err)
	}
	got, err = discoverHostGitConfig(home)
	if err != nil {
		t.Fatalf("discoverHostGitConfig() error = %v", err)
	}
	want, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve Git config fixture: %v", err)
	}
	if got != want {
		t.Fatalf("discoverHostGitConfig() = %q, want %q", got, want)
	}
}

func TestDiscoverHostGitConfigRejectsDirectory(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".gitconfig"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := discoverHostGitConfig(home)
	if err == nil || !strings.Contains(err.Error(), "is not a regular file") {
		t.Fatalf("discoverHostGitConfig() error = %v", err)
	}
}

func TestIsTerminalRejectsDevNull(t *testing.T) {
	t.Parallel()
	file, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	if terminal.IsReader(file) {
		t.Fatalf("terminal.IsReader(%s) = true", os.DevNull)
	}
}

func testPlan() Plan {
	return Plan{
		ProjectRoot: "/sources/feature worktree",
		WorkingDir:  "/sources/feature worktree/nested",
		Mounts: []Mount{
			{Source: "/sources/primary", Target: "/sources/primary", ReadOnly: true},
			{Source: "/sources/primary/.git", Target: "/sources/primary/.git"},
			{Source: "/sources/feature worktree", Target: "/sources/feature worktree"},
		},
	}
}

func simplePlan() Plan {
	return Plan{
		ProjectRoot: "/project",
		WorkingDir:  "/project/nested",
		Mounts:      []Mount{{Source: "/project", Target: "/project"}},
	}
}

func testDocker(runner CommandRunner) *Docker {
	return &Docker{
		Binary:    "docker",
		GOOS:      "linux",
		Runner:    runner,
		HostUID:   1000,
		HostGID:   1000,
		HostUser:  "developer",
		HostGroup: "developers",
		HostHome:  "/home/developer",
		// Focused Launch tests use fake paths, so inject a resolver instead of touching the
		// filesystem. User-state resolution itself is covered in userstate_test.go.
		resolveUserState: func(Plan) (UserState, error) {
			return UserState{CodexHome: "/home/developer/.codex", PersonalSkills: PersonalSkillsAbsent}, nil
		},
	}
}

func testUserState() UserState {
	return UserState{CodexHome: "/home/developer/.codex", PersonalSkills: PersonalSkillsAbsent}
}

func mustContainerName(t *testing.T, hostUID int, projectRoot string) string {
	t.Helper()
	name, err := ProjectContainerName(hostUID, projectRoot)
	if err != nil {
		t.Fatalf("ProjectContainerName() error = %v", err)
	}
	return name
}

func matchingLabels(projectRoot string, hostUID int) map[string]string {
	return map[string]string{
		managedLabel:         managedLabelValue,
		projectPathLabel:     projectRoot,
		hostUIDLabel:         strconv.Itoa(hostUID),
		managerProtocolLabel: "1",
		codexHomeLabel:       "/home/developer/.codex",
		personalSkillsLabel:  PersonalSkillsAbsent,
	}
}

func assertLabel(t *testing.T, args []string, name string, want string) {
	t.Helper()
	target := name + "=" + want
	for index, arg := range args {
		if arg == "--label" && index+1 < len(args) && args[index+1] == target {
			return
		}
	}
	t.Fatalf("args missing --label %q: %#v", target, args)
}

func inspectionJSON(
	t *testing.T,
	containerID string,
	running bool,
	status string,
	labels map[string]string,
) []byte {
	t.Helper()
	inspection := containerInspection{ID: containerID}
	inspection.Config.Labels = labels
	inspection.State.Running = running
	inspection.State.Status = status
	data, err := json.Marshal([]containerInspection{inspection})
	if err != nil {
		t.Fatalf("marshal inspection: %v", err)
	}
	return data
}

func containerNotFound() commandResult {
	return commandResult{
		output: []byte("Error: No such container: codex-safe-test"),
		err:    fakeExitError{1},
	}
}

func containsSequence(values []string, sequence ...string) bool {
	for index := 0; index+len(sequence) <= len(values); index++ {
		if reflect.DeepEqual(values[index:index+len(sequence)], sequence) {
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
	if !reflect.DeepEqual(calls[0][len(calls[0])-len(wantSuffix):], wantSuffix) {
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

func (runner *fakeCommandRunner) CombinedOutput(
	_ context.Context,
	name string,
	arguments ...string,
) ([]byte, error) {
	runner.combinedCalls = append(runner.combinedCalls, append([]string{name}, arguments...))
	if len(runner.outputs) == 0 {
		return nil, errors.New("unexpected CombinedOutput call")
	}
	result := runner.outputs[0]
	runner.outputs = runner.outputs[1:]
	return result.output, result.err
}

func (runner *fakeCommandRunner) Run(
	_ context.Context,
	name string,
	arguments []string,
	_ io.Reader,
	_ io.Writer,
	_ io.Writer,
) error {
	runner.runCalls = append(runner.runCalls, append([]string{name}, arguments...))
	if len(runner.runErrors) == 0 {
		return nil
	}
	err := runner.runErrors[0]
	runner.runErrors = runner.runErrors[1:]
	return err
}

type fakeExitError struct {
	code int
}

func (err fakeExitError) Error() string {
	return "exit failure"
}

func (err fakeExitError) ExitCode() int {
	return err.code
}
