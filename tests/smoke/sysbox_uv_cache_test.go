package smoke_test

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/base64"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
)

const approvedUVSmokeImage = "ghcr.io/astral-sh/uv:0.8.14-python3.13-bookworm-slim@sha256:5b651a2084b59293d8a9327a5b91b2779c955ebeac9bfd40f95fe91e9bc06c43"

// TestSysboxConfiguredUVCacheBind proves the real container receives the configured uv bind and routing value.
// It deliberately does not require uv in the base image; the approved-image scenario below covers tool-level reuse.
func TestSysboxConfiguredUVCacheBind(t *testing.T) {
	if os.Getenv(goSmokeEnv) != "1" {
		t.Skipf("set %s=1 to run the real Sysbox uv cache-bind test", goSmokeEnv)
	}
	fixture := newSmokeFixture(t)
	cache := filepath.Join(fixture.project.hostHome, ".cache", "uv")
	require.NoError(t, os.MkdirAll(cache, 0o755), "host uv cache must exist before launch")
	writeDependencyCacheConfigAt(t, fixture, fixture.project.worktree, []projectenv.DependencyCacheConfig{{
		Kind: projectenv.DependencyCacheUV, Source: cache,
	}})
	cleanupProjectImage(t, fixture)
	projectDockerfile := "ARG AGENTS_SAFE_BASE=" + goSmokeImage + "\n" +
		"FROM ${AGENTS_SAFE_BASE}\n" +
		"RUN test -z \"${UV_CACHE_DIR:-}\"\n" +
		"ENV UV_CACHE_DIR=/image-owned/uv\n"
	require.NoError(t, os.WriteFile(
		filepath.Join(fixture.project.worktree, projectenv.Directory, projectenv.DockerfileName),
		[]byte(projectDockerfile),
		0o600,
	), "uv project image must not receive a cache environment value at build time")

	report := filepath.Join(fixture.project.worktree, "uv-cache-bind.report")
	ready := filepath.Join(fixture.project.worktree, "uv-cache-bind.ready")
	release := filepath.Join(fixture.project.worktree, "uv-cache-bind.release")
	command := fixture.launcher.startDefault(fixture.project.worktree,
		"bash", "-c", `printf 'uv=%s\n' "$UV_CACHE_DIR" > "$1"
: > "$UV_CACHE_DIR/from-container"
: > "$2"
while [[ ! -e "$3" ]]; do sleep 1; done`,
		"bash", report, ready, release,
	)
	fixture.waitForFile(ready, command)
	observed := parseReport(t, report)
	require.Equal(t, cache, observed["uv"], "managed UV_CACHE_DIR must reach the public launcher command")
	inspection := fixture.docker.inspectContainer()
	require.Equal(t, cache, inspection.Config.Labels["agents-safe.uv-cache"], "uv label must name the physical source")
	requireMount(t, inspection, cache, cache, true)
	nestedReport := filepath.Join(fixture.project.worktree, "uv-cache-bind-nested.report")
	nested := fixture.launcher.startDefault(fixture.project.worktree,
		"bash", "-c", `docker run --rm "$1" sh -c 'printf "%s" "${UV_CACHE_DIR-unset}"' > "$2"`,
		"bash", nestedImage, nestedReport,
	)
	nested.requireExit(t, "nested Docker uv cache isolation command")
	require.Equal(t, "unset", readFile(t, nestedReport), "nested Docker must not inherit UV_CACHE_DIR")

	fixture.release(release, command, "configured uv cache command")
	fixture.docker.waitForContainerRemoval()
	_, err := os.Stat(filepath.Join(cache, "from-container"))
	require.NoError(t, err, "host must observe the session cache write")
	requireHostOwnership(t, cache)
}

// TestSysboxUVHostCacheReuse proves the approved toolchain reuses one cache in both directions without a live index.
func TestSysboxUVHostCacheReuse(t *testing.T) {
	if os.Getenv(goSmokeEnv) != "1" {
		t.Skipf("set %s=1 to run the real Sysbox uv cache test", goSmokeEnv)
	}
	requireHostUV(t)
	fixture := newSmokeFixture(t)
	sharedCache := filepath.Join(fixture.project.hostHome, ".cache", "uv-shared")
	firstEmptyCache := filepath.Join(fixture.project.hostHome, ".cache", "uv-empty-first")
	secondEmptyCache := filepath.Join(fixture.project.hostHome, ".cache", "uv-empty-second")
	for _, path := range []string{sharedCache, firstEmptyCache, secondEmptyCache} {
		require.NoError(t, os.MkdirAll(path, 0o755), "cache directory must exist before launch")
	}
	makeTestCacheRemovable(t, sharedCache, firstEmptyCache, secondEmptyCache)
	writeUVCacheProjectEnvironment(t, fixture, sharedCache)

	indexRoot := filepath.Join(fixture.project.worktree, "uv-simple-index")
	firstRequirement := writeUVWheel(t, indexRoot, "uvhostcachepkg", "host-to-container")
	secondRequirement := writeUVWheel(t, indexRoot, "uvcontainercachepkg", "container-to-host")

	hostServer := startLoopbackFileServer(t, indexRoot)
	hostSeedVenv := filepath.Join(fixture.project.worktree, ".venv-host-seed")
	createHostUVVenv(t, fixture.project.worktree, sharedCache, hostSeedVenv)
	installHostUVPackage(t, fixture.project.worktree, sharedCache, hostSeedVenv, hostServer.URL, firstRequirement, false)
	requirePythonValue(t, filepath.Join(hostSeedVenv, "bin", "python"), "uvhostcachepkg", "host-to-container")
	hostServer.Close(t)
	require.NoError(t, os.RemoveAll(hostSeedVenv), "native seed environment must be removed before offline reuse")

	writeDependencyCacheConfigAt(t, fixture, fixture.project.worktree, []projectenv.DependencyCacheConfig{{
		Kind: projectenv.DependencyCacheUV, Source: firstEmptyCache,
	}})
	firstControlVenv := filepath.Join(fixture.project.worktree, ".venv-container-empty")
	firstControl := fixture.launcher.startDefault(fixture.project.worktree,
		"bash", "-c", uvOfflineInstallScript,
		"bash", firstControlVenv, hostServer.URL, firstRequirement, filepath.Join(fixture.project.worktree, "first-control.report"),
	)
	requireProcessFailure(t, firstControl, "empty container uv cache control")
	fixture.docker.waitForContainerRemoval()
	require.NoError(t, os.RemoveAll(firstControlVenv), "empty control environment must be removed")

	writeUVCacheProjectEnvironment(t, fixture, sharedCache)
	containerVenv := filepath.Join(fixture.project.worktree, ".venv-container-reuse")
	containerReport := filepath.Join(fixture.project.worktree, "host-to-container.report")
	containerReuse := fixture.launcher.startDefault(fixture.project.worktree,
		"bash", "-c", uvOfflineInstallScript,
		"bash", containerVenv, hostServer.URL, firstRequirement, containerReport,
	)
	containerReuse.requireExit(t, "offline host-to-container uv cache reuse")
	require.Equal(t, "host-to-container\n", readFile(t, containerReport))
	fixture.docker.waitForContainerRemoval()

	containerSeedVenv := filepath.Join(fixture.project.worktree, ".venv-container-seed")
	containerSeedReport := filepath.Join(fixture.project.worktree, "container-seed.report")
	containerSeed := fixture.launcher.startDefault(fixture.project.worktree,
		"bash", "-c", uvLoopbackInstallScript,
		"bash", hostServer.Port, indexRoot, containerSeedVenv, hostServer.URL, secondRequirement, containerSeedReport,
	)
	containerSeed.requireExit(t, "container uv cache seed through container loopback")
	require.Equal(t, "container-to-host\n", readFile(t, containerSeedReport))
	fixture.docker.waitForContainerRemoval()

	secondControlVenv := filepath.Join(fixture.project.worktree, ".venv-host-empty")
	createHostUVVenv(t, fixture.project.worktree, secondEmptyCache, secondControlVenv)
	requireHostUVInstallFailure(t, fixture.project.worktree, secondEmptyCache, secondControlVenv, hostServer.URL, secondRequirement)
	require.NoError(t, os.RemoveAll(secondControlVenv), "native empty control environment must be removed")

	hostReuseVenv := filepath.Join(fixture.project.worktree, ".venv-host-reuse")
	createHostUVVenv(t, fixture.project.worktree, sharedCache, hostReuseVenv)
	installHostUVPackage(t, fixture.project.worktree, sharedCache, hostReuseVenv, hostServer.URL, secondRequirement, true)
	requirePythonValue(t, filepath.Join(hostReuseVenv, "bin", "python"), "uvcontainercachepkg", "container-to-host")

	containerColdVenv := filepath.Join(fixture.project.worktree, ".venv-container-cold")
	containerColdReport := filepath.Join(fixture.project.worktree, "container-cold.report")
	containerCold := fixture.launcher.startDefault(fixture.project.worktree,
		"bash", "-c", uvOfflineInstallScript,
		"bash", containerColdVenv, hostServer.URL, secondRequirement, containerColdReport,
	)
	containerCold.requireExit(t, "offline cold container uv cache reuse")
	require.Equal(t, "container-to-host\n", readFile(t, containerColdReport))
	fixture.docker.waitForContainerRemoval()
	requireHostOwnership(t, sharedCache)
}

// TestSysboxConcurrentUVCacheWorktrees proves two session containers can write one uv cache without launcher locking.
func TestSysboxConcurrentUVCacheWorktrees(t *testing.T) {
	if os.Getenv(goSmokeEnv) != "1" {
		t.Skipf("set %s=1 to run the real Sysbox uv cache test", goSmokeEnv)
	}
	fixture := newSmokeFixture(t)
	sharedCache := filepath.Join(fixture.project.hostHome, ".cache", "uv-concurrent")
	require.NoError(t, os.MkdirAll(sharedCache, 0o755), "shared uv cache must exist before launch")
	makeTestCacheRemovable(t, sharedCache)
	parallelWorktree := addConcurrentUVCacheWorktree(t, fixture)
	writeUVCacheProjectEnvironment(t, fixture, sharedCache)
	writeUVCacheProjectEnvironmentAt(t, fixture, parallelWorktree, sharedCache)

	firstIndex := filepath.Join(fixture.project.worktree, "uv-concurrent-index")
	secondIndex := filepath.Join(parallelWorktree, "uv-concurrent-index")
	firstRequirement := writeUVWheel(t, firstIndex, "uvconcurrentfirstpkg", "first")
	secondRequirement := writeUVWheel(t, secondIndex, "uvconcurrentsecondpkg", "second")
	port := reserveLoopbackPort(t)
	url := "http://127.0.0.1:" + port + "/simple"

	firstReady := filepath.Join(fixture.project.worktree, "uv-concurrent-first.ready")
	firstRelease := filepath.Join(fixture.project.worktree, "uv-concurrent-first.release")
	secondReady := filepath.Join(parallelWorktree, "uv-concurrent-second.ready")
	secondRelease := filepath.Join(parallelWorktree, "uv-concurrent-second.release")
	firstReport := filepath.Join(fixture.project.worktree, "uv-concurrent-first.report")
	secondReport := filepath.Join(parallelWorktree, "uv-concurrent-second.report")
	first := fixture.launcher.startDefault(fixture.project.worktree,
		"bash", "-c", uvConcurrentInstallScript,
		"bash", port, firstIndex, filepath.Join(fixture.project.worktree, ".venv-concurrent"), url, firstRequirement, firstReport, firstReady, firstRelease,
	)
	second := fixture.launcher.startDefault(parallelWorktree,
		"bash", "-c", uvConcurrentInstallScript,
		"bash", port, secondIndex, filepath.Join(parallelWorktree, ".venv-concurrent"), url, secondRequirement, secondReport, secondReady, secondRelease,
	)
	fixture.waitForFile(firstReady, first)
	fixture.waitForFile(secondReady, second)
	require.NoError(t, os.WriteFile(firstRelease, nil, 0o600), "first concurrent release marker must be writable")
	require.NoError(t, os.WriteFile(secondRelease, nil, 0o600), "second concurrent release marker must be writable")
	first.requireExit(t, "first concurrent uv cache command")
	second.requireExit(t, "second concurrent uv cache command")
	require.Equal(t, "first\n", readFile(t, firstReport))
	require.Equal(t, "second\n", readFile(t, secondReport))
	requireHostOwnership(t, sharedCache)
	fixture.docker.waitForContainerRemoval()
	parallelContainer := launcher.ProjectContainerName(os.Getuid(), parallelWorktree)
	waitForManagedContainerRemoval(t, fixture, parallelContainer)
}

// TestSysboxUVCacheConfigMismatch proves an active session rejects a changed uv cache identity without replacement.
func TestSysboxUVCacheConfigMismatch(t *testing.T) {
	if os.Getenv(goSmokeEnv) != "1" {
		t.Skipf("set %s=1 to run the real Sysbox uv cache test", goSmokeEnv)
	}
	fixture := newSmokeFixture(t)
	cache := filepath.Join(fixture.project.hostHome, ".cache", "uv-mismatch")
	changedCache := filepath.Join(fixture.project.hostHome, ".cache", "uv-mismatch-changed")
	for _, path := range []string{cache, changedCache} {
		require.NoError(t, os.MkdirAll(path, 0o755), "uv cache must exist before launch")
	}
	writeDependencyCacheConfigAt(t, fixture, fixture.project.worktree, []projectenv.DependencyCacheConfig{{
		Kind: projectenv.DependencyCacheUV, Source: cache,
	}})
	ready := filepath.Join(fixture.project.worktree, "uv-cache-mismatch.ready")
	release := filepath.Join(fixture.project.worktree, "uv-cache-mismatch.release")
	active := fixture.launcher.startAgents(fixture.project.worktree,
		"bash", "-c", `: > "$1"; while [[ ! -e "$2" ]]; do sleep 1; done`, "bash", ready, release,
	)
	fixture.waitForFile(ready, active)
	before := fixture.docker.inspectContainer()
	writeDependencyCacheConfigAt(t, fixture, fixture.project.worktree, []projectenv.DependencyCacheConfig{{
		Kind: projectenv.DependencyCacheUV, Source: changedCache,
	}})
	mismatch := fixture.launcher.startAgents(fixture.project.worktree, "true")
	requireProcessFailure(t, mismatch, "changed uv cache configuration")
	require.Contains(t, mismatch.diagnostics(), "creation fingerprint", "mismatch must identify the immutable session contract")
	after := fixture.docker.inspectContainer()
	require.Equal(t, before.ID, after.ID, "mismatch must neither replace nor create a managed container")
	require.True(t, after.State.Running, "mismatch must leave the active session running")
	require.Equal(t, cache, after.Config.Labels["agents-safe.uv-cache"], "active session must retain its original uv source")
	requireMount(t, after, cache, cache, true)
	fixture.release(release, active, "original uv cache configuration command")
	fixture.docker.waitForContainerRemoval()
}

func writeUVCacheProjectEnvironment(t *testing.T, fixture *smokeFixture, cache string) {
	writeUVCacheProjectEnvironmentAt(t, fixture, fixture.project.worktree, cache)
}

func writeUVCacheProjectEnvironmentAt(t *testing.T, fixture *smokeFixture, worktree, cache string) {
	t.Helper()
	writeDependencyCacheConfigAt(t, fixture, worktree, []projectenv.DependencyCacheConfig{{
		Kind: projectenv.DependencyCacheUV, Source: cache,
	}})
	cleanupProjectImageAt(t, fixture, worktree)
	dockerfile := "ARG AGENTS_SAFE_BASE=" + goSmokeImage + "\n" +
		"FROM " + approvedUVSmokeImage + " AS uv-toolchain\n" +
		"FROM ${AGENTS_SAFE_BASE}\n" +
		"RUN test -z \"${UV_CACHE_DIR:-}\"\n" +
		"COPY --from=uv-toolchain /usr/local /usr/local\n" +
		"ENV PATH=/usr/local/bin:${PATH}\n" +
		"ENV UV_CACHE_DIR=/image-owned/uv\n" +
		"RUN uv --version | grep -qx 'uv 0.8.14' && python --version | grep -q '^Python 3.13.'\n"
	require.NoError(t, os.WriteFile(filepath.Join(worktree, projectenv.Directory, projectenv.DockerfileName), []byte(dockerfile), 0o600),
		"uv-capable project Dockerfile must be written")
}

func requireHostUV(t *testing.T) {
	t.Helper()
	for _, command := range [][]string{{"uv", "--version"}, {"python3", "--version"}} {
		output, err := exec.Command(command[0], command[1:]...).CombinedOutput()
		if err != nil {
			t.Skipf("real uv cache reuse requires host %s: %v\n%s", command[0], err, output)
		}
	}
}

func createHostUVVenv(t *testing.T, directory, cache, venv string) {
	t.Helper()
	runHostUV(t, directory, cache, "venv", "--no-project", "--no-config", "--no-python-downloads", "--python", "python3", venv)
}

func installHostUVPackage(t *testing.T, directory, cache, venv, indexURL, requirement string, offline bool) {
	t.Helper()
	runHostUV(t, directory, cache, hostUVInstallArgs(venv, indexURL, requirement, offline)...)
}

func requireHostUVInstallFailure(t *testing.T, directory, cache, venv, indexURL, requirement string) {
	t.Helper()
	command := exec.Command("uv", hostUVInstallArgs(venv, indexURL, requirement, true)...)
	command.Dir = directory
	command.Env = uvEnvironment(cache)
	output, err := command.CombinedOutput()
	require.Error(t, err, "empty native uv cache must not satisfy %s: %s", requirement, output)
}

func hostUVInstallArgs(venv, indexURL, requirement string, offline bool) []string {
	args := []string{"pip", "install", "--no-config", "--no-build", "--index-url", indexURL,
		"--python", filepath.Join(venv, "bin", "python")}
	if offline {
		args = append(args, "--offline")
	}
	return append(args, requirement)
}

func runHostUV(t *testing.T, directory, cache string, args ...string) {
	t.Helper()
	command := exec.Command("uv", args...)
	command.Dir = directory
	command.Env = uvEnvironment(cache)
	output, err := command.CombinedOutput()
	require.NoError(t, err, "uv %s must succeed: %s", strings.Join(args, " "), output)
}

func uvEnvironment(cache string) []string {
	result := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "UV_") {
			continue
		}
		result = append(result, entry)
	}
	return append(result, "UV_CACHE_DIR="+cache, "UV_NO_PROGRESS=1")
}

func requirePythonValue(t *testing.T, python, module, want string) {
	t.Helper()
	output, err := exec.Command(python, "-c", "import "+module+"; print("+module+".VALUE)").CombinedOutput()
	require.NoError(t, err, "%s must import: %s", module, output)
	require.Equal(t, want+"\n", string(output))
}

func requireProcessFailure(t *testing.T, process *launcherProcess, description string) {
	t.Helper()
	select {
	case <-process.done:
		require.Error(t, process.err, "%s must fail\n%s", description, process.diagnostics())
	case <-time.After(commandTimeout):
		t.Fatalf("timed out waiting for %s\n%s", description, process.diagnostics())
	}
}

type loopbackFileServer struct {
	URL    string
	Port   string
	server *http.Server
}

func startLoopbackFileServer(t *testing.T, root string) *loopbackFileServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "loopback package index must bind")
	_, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err, "loopback package index address must include a port")
	server := &http.Server{Handler: http.FileServer(http.Dir(root))}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return &loopbackFileServer{URL: "http://127.0.0.1:" + port + "/simple", Port: port, server: server}
}

func (server *loopbackFileServer) Close(t *testing.T) {
	t.Helper()
	require.NoError(t, server.server.Close(), "loopback package index must stop before offline reuse")
}

func reserveLoopbackPort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "loopback port must reserve")
	defer listener.Close()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err, "reserved loopback address must include a port")
	return port
}

func addConcurrentUVCacheWorktree(t *testing.T, fixture *smokeFixture) string {
	t.Helper()
	worktree := filepath.Join(fixture.project.root, "parallel uv cache worktree")
	runInDir(t, fixture.project.primary, "git", "worktree", "add", "-b", "smoke/uv-cache-parallel", worktree)
	t.Cleanup(func() {
		command := exec.Command("git", "worktree", "remove", "--force", worktree)
		command.Dir = fixture.project.primary
		_ = command.Run()
	})
	return worktree
}

type uvWheelEntry struct {
	path string
	data []byte
}

func writeUVWheel(t *testing.T, indexRoot, name, value string) string {
	t.Helper()
	const version = "1.0.0"
	distInfo := name + "-" + version + ".dist-info"
	filename := name + "-" + version + "-py3-none-any.whl"
	entries := []uvWheelEntry{
		{path: name + "/__init__.py", data: []byte("VALUE = " + strconv.Quote(value) + "\n")},
		{path: distInfo + "/METADATA", data: []byte("Metadata-Version: 2.1\nName: " + name + "\nVersion: " + version + "\n\n")},
		{path: distInfo + "/WHEEL", data: []byte("Wheel-Version: 1.0\nGenerator: agents-safe smoke\nRoot-Is-Purelib: true\nTag: py3-none-any\n")},
	}
	packageDirectory := filepath.Join(indexRoot, "packages")
	require.NoError(t, os.MkdirAll(packageDirectory, 0o755), "wheel package directory must be created")
	archive, err := os.Create(filepath.Join(packageDirectory, filename))
	require.NoError(t, err, "wheel archive must be created")
	writer := zip.NewWriter(archive)
	record := make([]string, 0, len(entries)+1)
	for _, entry := range entries {
		file, createErr := writer.Create(entry.path)
		require.NoError(t, createErr, "wheel entry %q must be created", entry.path)
		_, writeErr := file.Write(entry.data)
		require.NoError(t, writeErr, "wheel entry %q must be written", entry.path)
		digest := sha256.Sum256(entry.data)
		record = append(record, entry.path+",sha256="+base64.RawURLEncoding.EncodeToString(digest[:])+","+strconv.Itoa(len(entry.data)))
	}
	recordPath := distInfo + "/RECORD"
	record = append(record, recordPath+",,")
	recordFile, err := writer.Create(recordPath)
	require.NoError(t, err, "wheel record must be created")
	_, err = recordFile.Write([]byte(strings.Join(record, "\n") + "\n"))
	require.NoError(t, err, "wheel record must be written")
	require.NoError(t, writer.Close(), "wheel archive must close")
	require.NoError(t, archive.Close(), "wheel file must close")

	indexDirectory := filepath.Join(indexRoot, "simple", name)
	require.NoError(t, os.MkdirAll(indexDirectory, 0o755), "simple-index package directory must be created")
	page := "<!doctype html><html><body><a href=\"../../packages/" + filename + "\">" + filename + "</a></body></html>\n"
	require.NoError(t, os.WriteFile(filepath.Join(indexDirectory, "index.html"), []byte(page), 0o600), "simple-index page must be written")
	return name + "==" + version
}

const uvOfflineInstallScript = `venv=$1
index=$2
requirement=$3
report=$4
uv venv --no-project --no-config --no-python-downloads --python python "$venv"
uv pip install --offline --no-config --no-build --index-url "$index" --python "$venv/bin/python" "$requirement"
"$venv/bin/python" -c "import ${requirement%%==*}; print(${requirement%%==*}.VALUE)" > "$report"`

const uvStartLoopbackIndexScript = `python -m http.server "$port" --bind 127.0.0.1 --directory "$root" >/tmp/uv-smoke-index.log 2>&1 &
server=$!
trap 'kill "$server" 2>/dev/null || true; wait "$server" 2>/dev/null || true' EXIT
for attempt in {1..50}; do
  if python - "$port" <<'PY'
import socket
import sys
with socket.create_connection(("127.0.0.1", int(sys.argv[1])), timeout=0.2):
    pass
PY
  then break; fi
  sleep 0.1
done
`

const uvLoopbackInstallScript = `port=$1
root=$2
venv=$3
index=$4
requirement=$5
report=$6
` + uvStartLoopbackIndexScript + `uv venv --no-project --no-config --no-python-downloads --python python "$venv"
uv pip install --no-config --no-build --index-url "$index" --python "$venv/bin/python" "$requirement"
"$venv/bin/python" -c "import ${requirement%%==*}; print(${requirement%%==*}.VALUE)" > "$report"`

const uvConcurrentInstallScript = `port=$1
root=$2
venv=$3
index=$4
requirement=$5
report=$6
ready=$7
release=$8
` + uvStartLoopbackIndexScript + `: > "$ready"
while [[ ! -e "$release" ]]; do sleep 0.1; done
uv venv --no-project --no-config --no-python-downloads --python python "$venv"
uv pip install --no-config --no-build --index-url "$index" --python "$venv/bin/python" "$requirement"
"$venv/bin/python" -c "import ${requirement%%==*}; print(${requirement%%==*}.VALUE)" > "$report"`
