package smoke_test

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"
	"github.com/vkuptcov/agents-safe-environment/internal/gitproject"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
)

// TestSysboxGoHostCaches proves the Go-only host-cache contract at the real Docker boundary. It intentionally uses
// a project image: the base runtime remains free of a Go toolchain.
func TestSysboxGoHostCaches(t *testing.T) {
	if os.Getenv(goSmokeEnv) != "1" {
		t.Skipf("set %s=1 to run the real Sysbox Go cache test", goSmokeEnv)
	}
	fixture := newSmokeFixture(t)
	buildCache := filepath.Join(fixture.project.hostHome, ".cache", "go-build")
	moduleCache := filepath.Join(fixture.project.hostHome, "go", "pkg", "mod")
	for _, path := range []string{buildCache, moduleCache} {
		require.NoError(t, os.MkdirAll(path, 0o755), "host cache must exist before init/launch")
	}
	writeGoCacheProjectEnvironment(t, fixture, buildCache, moduleCache)
	cleanupGoProjectImage(t, fixture)
	makeTestCacheRemovable(t, buildCache, moduleCache)
	proxy := writeGoModuleProxy(t, fixture.project.worktree)
	writeGoCacheClient(t, fixture.project.worktree)
	hostGoEnvironment := []string{
		"GOCACHE=" + buildCache,
		"GOMODCACHE=" + moduleCache,
		"GOPROXY=file://" + proxy,
		"GOSUMDB=off",
	}

	report := filepath.Join(fixture.project.worktree, "go-caches.report")
	ready := filepath.Join(fixture.project.worktree, "go-caches.ready")
	release := filepath.Join(fixture.project.worktree, "go-caches.release")
	command := fixture.launcher.startDefault(fixture.project.worktree,
		"bash", "-c", `printf 'build=%s\nmodules=%s\n' "$GOCACHE" "$GOMODCACHE" > "$1"
: > "$2"
while [[ ! -e "$3" ]]; do sleep 1; done`, "bash", report, ready, release,
	)
	fixture.waitForFile(ready, command)
	lines := strings.Split(strings.TrimSpace(readFile(t, report)), "\n")
	require.Contains(t, lines, "build="+buildCache, "managed GOCACHE must override image default")
	require.Contains(t, lines, "modules="+moduleCache, "managed GOMODCACHE must override image default")
	requireMount(t, fixture.docker.inspectContainer(), buildCache, buildCache, true)
	requireMount(t, fixture.docker.inspectContainer(), moduleCache, moduleCache, true)
	runGoWithEnvironment(t, fixture.project.worktree, hostGoEnvironment, "mod", "tidy")
	runGoWithEnvironment(t, fixture.project.worktree, hostGoEnvironment, "run", ".")

	goReport := filepath.Join(fixture.project.worktree, "go-caches-go-env.report")
	goCommand := fixture.launcher.startDefault(fixture.project.worktree,
		"bash", "-c", `GOPROXY=off GOSUMDB=off go run . > "$1"
go env GOCACHE >> "$1"
go env GOMODCACHE >> "$1"
: > "$2/from-container"
: > "$3/from-container"`, "bash", goReport, buildCache, moduleCache,
	)
	goCommand.requireExit(t, "reused Go cache command")
	goLines := strings.Split(strings.TrimSpace(readFile(t, goReport)), "\n")
	require.Equal(t, []string{"cached dependency", buildCache, moduleCache}, goLines,
		"offline container Go command must use the configured cache targets")
	nestedReport := filepath.Join(fixture.project.worktree, "go-caches-nested.report")
	nested := fixture.launcher.startDefault(fixture.project.worktree,
		"bash", "-c", `docker run --rm "$1" sh -c 'printf "%s|%s" "${GOCACHE-unset}" "${GOMODCACHE-unset}"' > "$2"`,
		"bash", nestedImage, nestedReport,
	)
	nested.requireExit(t, "nested Docker cache isolation command")
	require.Equal(t, "unset|unset", readFile(t, nestedReport), "nested Docker must not inherit Go cache variables")

	fixture.release(release, command, "Go cache command")
	fixture.docker.waitForContainerRemoval()
	for _, path := range []string{filepath.Join(buildCache, "from-container"), filepath.Join(moduleCache, "from-container")} {
		_, err := os.Stat(path)
		require.NoError(t, err, "host must observe cache state written in the session")
	}
	requireHostOwnership(t, buildCache, moduleCache)
	runGoWithEnvironment(t, fixture.project.worktree, []string{
		"GOCACHE=" + buildCache,
		"GOMODCACHE=" + moduleCache,
		"GOPROXY=off",
		"GOSUMDB=off",
	}, "run", ".")

	coldReport := filepath.Join(fixture.project.worktree, "go-caches-cold.report")
	cold := fixture.launcher.startDefault(fixture.project.worktree,
		"bash", "-c", "GOPROXY=off GOSUMDB=off go run . > \"$1\"", "bash", coldReport,
	)
	cold.requireExit(t, "cold offline Go cache command")
	require.Equal(t, "cached dependency\n", readFile(t, coldReport), "new cold session must reuse the host module cache offline")
	fixture.docker.waitForContainerRemoval()
}

// TestSysboxGoCacheConfigMismatch proves changing cache identity never replaces or silently reuses a live session.
func TestSysboxGoCacheConfigMismatch(t *testing.T) {
	if os.Getenv(goSmokeEnv) != "1" {
		t.Skipf("set %s=1 to run the real Sysbox Go cache test", goSmokeEnv)
	}
	fixture := newSmokeFixture(t)
	buildCache := filepath.Join(fixture.project.hostHome, ".cache", "go-build")
	moduleCache := filepath.Join(fixture.project.hostHome, "go", "pkg", "mod")
	for _, path := range []string{buildCache, moduleCache} {
		require.NoError(t, os.MkdirAll(path, 0o755), "host cache must exist before launch")
	}
	writeGoCacheConfig(t, fixture, buildCache, moduleCache)

	ready := filepath.Join(fixture.project.worktree, "go-cache-mismatch.ready")
	release := filepath.Join(fixture.project.worktree, "go-cache-mismatch.release")
	active := fixture.launcher.startAgents(fixture.project.worktree,
		"bash", "-c", `: > "$1"; while [[ ! -e "$2" ]]; do sleep 1; done`, "bash", ready, release,
	)
	fixture.waitForFile(ready, active)
	before := fixture.docker.inspectContainer()
	changedBuildCache := filepath.Join(fixture.project.hostHome, ".cache", "go-build-changed")
	changedModuleCache := filepath.Join(fixture.project.hostHome, "go", "pkg", "mod-changed")
	for _, path := range []string{changedBuildCache, changedModuleCache} {
		require.NoError(t, os.MkdirAll(path, 0o755), "changed host cache must exist before launch")
	}
	writeGoCacheConfig(t, fixture, changedBuildCache, changedModuleCache)
	mismatch := fixture.launcher.startAgents(fixture.project.worktree, "true")
	select {
	case <-mismatch.done:
		require.Error(t, mismatch.err, "changed cache configuration must reject reuse")
		require.Contains(t, mismatch.diagnostics(), "creation fingerprint", "rejection must identify the immutable session contract")
	case <-time.After(commandTimeout):
		t.Fatal("timed out waiting for changed cache configuration rejection")
	}
	after := fixture.docker.inspectContainer()
	require.Equal(t, before.ID, after.ID, "mismatch must neither replace nor create a managed container")
	require.True(t, after.State.Running, "mismatch must leave the active session running")
	requireMount(t, after, buildCache, buildCache, true)
	requireMount(t, after, moduleCache, moduleCache, true)
	fixture.release(release, active, "original Go cache configuration command")
	fixture.docker.waitForContainerRemoval()
}

// TestSysboxConcurrentGoCacheWorktrees proves that independent worktrees may compile concurrently against one pair
// of Go cache directories. The launcher must not introduce a cache-level lock between their distinct sessions.
func TestSysboxConcurrentGoCacheWorktrees(t *testing.T) {
	if os.Getenv(goSmokeEnv) != "1" {
		t.Skipf("set %s=1 to run the real Sysbox Go cache test", goSmokeEnv)
	}
	fixture := newSmokeFixture(t)
	buildCache := filepath.Join(fixture.project.hostHome, ".cache", "go-build")
	moduleCache := filepath.Join(fixture.project.hostHome, "go", "pkg", "mod")
	for _, path := range []string{buildCache, moduleCache} {
		require.NoError(t, os.MkdirAll(path, 0o755), "shared host cache must exist before launch")
	}
	writeGoCacheProjectEnvironment(t, fixture, buildCache, moduleCache)
	cleanupGoProjectImage(t, fixture)
	makeTestCacheRemovable(t, buildCache, moduleCache)
	proxy := writeGoModuleProxy(t, fixture.project.worktree)
	writeGoCacheClient(t, fixture.project.worktree)
	parallelWorktree := addConcurrentGoCacheWorktree(t, fixture)
	writeGoCacheConfigAt(t, fixture, parallelWorktree, buildCache, moduleCache)
	cleanupGoProjectImageAt(t, fixture, parallelWorktree)

	firstReady := filepath.Join(fixture.project.worktree, "go-cache-first.ready")
	firstRelease := filepath.Join(fixture.project.worktree, "go-cache-first.release")
	secondReady := filepath.Join(parallelWorktree, "go-cache-second.ready")
	secondRelease := filepath.Join(parallelWorktree, "go-cache-second.release")
	first := fixture.launcher.startDefault(fixture.project.worktree,
		"bash", "-c", `: > "$1"; while [[ ! -e "$2" ]]; do sleep 1; done`, "bash", firstReady, firstRelease,
	)
	second := fixture.launcher.startDefault(parallelWorktree,
		"bash", "-c", `: > "$1"; while [[ ! -e "$2" ]]; do sleep 1; done`, "bash", secondReady, secondRelease,
	)
	fixture.waitForFile(firstReady, first)
	fixture.waitForFile(secondReady, second)
	runGoWithEnvironment(t, fixture.project.worktree, []string{
		"GOCACHE=" + buildCache,
		"GOMODCACHE=" + moduleCache,
		"GOPROXY=file://" + proxy,
		"GOSUMDB=off",
	}, "mod", "tidy")
	goSum, err := os.ReadFile(filepath.Join(fixture.project.worktree, "go.sum"))
	require.NoError(t, err, "host Go seed must write go.sum")
	require.NoError(t, os.WriteFile(filepath.Join(parallelWorktree, "go.sum"), goSum, 0o600), "parallel worktree must receive go.sum")
	runGoWithEnvironment(t, fixture.project.worktree, []string{
		"GOCACHE=" + buildCache,
		"GOMODCACHE=" + moduleCache,
	}, "clean", "-cache")

	firstReport := filepath.Join(fixture.project.worktree, "go-cache-first.report")
	secondReport := filepath.Join(parallelWorktree, "go-cache-second.report")
	firstBuild := fixture.launcher.startDefault(fixture.project.worktree,
		"bash", "-c", "GOPROXY=off GOSUMDB=off go run . > \"$1\"", "bash", firstReport,
	)
	secondBuild := fixture.launcher.startDefault(parallelWorktree,
		"bash", "-c", "GOPROXY=off GOSUMDB=off go run . > \"$1\"", "bash", secondReport,
	)
	firstBuild.requireExit(t, "first concurrent Go cache command")
	secondBuild.requireExit(t, "second concurrent Go cache command")
	require.Equal(t, "cached dependency\n", readFile(t, firstReport))
	require.Equal(t, "cached dependency\n", readFile(t, secondReport))
	requireHostOwnership(t, buildCache, moduleCache)

	fixture.release(firstRelease, first, "first concurrent Go cache holder")
	fixture.release(secondRelease, second, "second concurrent Go cache holder")
	fixture.docker.waitForContainerRemoval()
	parallelContainer := launcher.ProjectContainerName(os.Getuid(), parallelWorktree)
	waitForManagedContainerRemoval(t, fixture, parallelContainer)
}

// TestSysboxConfiguredGoCacheBinds isolates the public launcher cache-mount path from the test-only Go project image.
func TestSysboxConfiguredGoCacheBinds(t *testing.T) {
	if os.Getenv(goSmokeEnv) != "1" {
		t.Skipf("set %s=1 to run the real Sysbox Go cache test", goSmokeEnv)
	}
	fixture := newSmokeFixture(t)
	buildCache := filepath.Join(fixture.project.hostHome, ".cache", "go-build")
	moduleCache := filepath.Join(fixture.project.hostHome, "go", "pkg", "mod")
	for _, path := range []string{buildCache, moduleCache} {
		require.NoError(t, os.MkdirAll(path, 0o755), "host cache must exist before launch")
	}
	writeGoCacheConfig(t, fixture, buildCache, moduleCache)

	report := filepath.Join(fixture.project.worktree, "go-cache-binds.report")
	ready := filepath.Join(fixture.project.worktree, "go-cache-binds.ready")
	release := filepath.Join(fixture.project.worktree, "go-cache-binds.release")
	command := fixture.launcher.startAgents(fixture.project.worktree,
		"bash", "-c", `printf 'build=%s\nmodules=%s\n' "$GOCACHE" "$GOMODCACHE" > "$1"; : > "$2"; while [[ ! -e "$3" ]]; do sleep 1; done`,
		"bash", report, ready, release,
	)
	fixture.waitForFile(ready, command)
	observed := parseReport(t, report)
	require.Equal(t, buildCache, observed["build"], "managed GOCACHE must reach the public launcher command")
	require.Equal(t, moduleCache, observed["modules"], "managed GOMODCACHE must reach the public launcher command")
	requireMount(t, fixture.docker.inspectContainer(), buildCache, buildCache, true)
	requireMount(t, fixture.docker.inspectContainer(), moduleCache, moduleCache, true)

	fixture.release(release, command, "configured Go cache command")
	fixture.docker.waitForContainerRemoval()
}

// TestSysboxGoProjectImageWithoutCaches isolates the project Go image from cache mount configuration.
func TestSysboxGoProjectImageWithoutCaches(t *testing.T) {
	if os.Getenv(goSmokeEnv) != "1" {
		t.Skipf("set %s=1 to run the real Sysbox Go cache test", goSmokeEnv)
	}
	fixture := newSmokeFixture(t)
	cleanupGoProjectImage(t, fixture)
	contextPath := filepath.Join(fixture.project.worktree, projectenv.Directory)
	require.NoError(t, os.Mkdir(contextPath, 0o755), "project environment directory must be created")
	dockerfile := "ARG AGENTS_SAFE_BASE=" + goSmokeImage + "\n" +
		"FROM golang:1.26.0-bookworm@sha256:2a0ba12e116687098780d3ce700f9ce3cb340783779646aafbabed748fa6677c AS go-toolchain\n" +
		"FROM ${AGENTS_SAFE_BASE}\n" +
		"RUN test -z \"${GOCACHE:-}\" && test -z \"${GOMODCACHE:-}\"\n" +
		"COPY --from=go-toolchain /usr/local/go /usr/local/go\n" +
		"ENV PATH=/usr/local/go/bin:${PATH}\n"
	require.NoError(t, os.WriteFile(filepath.Join(contextPath, projectenv.DockerfileName), []byte(dockerfile), 0o600),
		"Go-capable project Dockerfile must be written")

	report := filepath.Join(fixture.project.worktree, "go-image.report")
	ready := filepath.Join(fixture.project.worktree, "go-image.ready")
	release := filepath.Join(fixture.project.worktree, "go-image.release")
	command := fixture.launcher.startDefault(fixture.project.worktree,
		"bash", "-c", `go env GOCACHE > "$1"; : > "$2"; while [[ ! -e "$3" ]]; do sleep 1; done`,
		"bash", report, ready, release,
	)
	fixture.waitForFile(ready, command)
	require.NotEmpty(t, strings.TrimSpace(readFile(t, report)), "Go tool must run in the project image")
	fixture.release(release, command, "Go project image command")
	fixture.docker.waitForContainerRemoval()
}

func writeGoCacheProjectEnvironment(t *testing.T, fixture *smokeFixture, buildCache, moduleCache string) {
	t.Helper()
	contextPath := filepath.Join(fixture.project.worktree, projectenv.Directory)
	require.NoError(t, os.Mkdir(contextPath, 0o755), "project environment directory must be created")
	writeGoCacheConfig(t, fixture, buildCache, moduleCache)

	dockerfile := "ARG AGENTS_SAFE_BASE=" + goSmokeImage + "\n" +
		"FROM golang:1.26.0-bookworm@sha256:2a0ba12e116687098780d3ce700f9ce3cb340783779646aafbabed748fa6677c AS go-toolchain\n" +
		"FROM ${AGENTS_SAFE_BASE}\n" +
		"COPY --from=go-toolchain /usr/local/go /usr/local/go\n" +
		"ENV PATH=/usr/local/go/bin:${PATH}\n" +
		"ENV GOCACHE=/image-owned/build GOMODCACHE=/image-owned/modules\n"
	require.NoError(t, os.WriteFile(filepath.Join(contextPath, projectenv.DockerfileName), []byte(dockerfile), 0o600),
		"Go-capable project Dockerfile must be written")
}

func writeGoCacheConfig(t *testing.T, fixture *smokeFixture, buildCache, moduleCache string) {
	writeGoCacheConfigAt(t, fixture, fixture.project.worktree, buildCache, moduleCache)
}

func writeGoCacheConfigAt(t *testing.T, fixture *smokeFixture, worktree, buildCache, moduleCache string) {
	t.Helper()
	contextPath := filepath.Join(worktree, projectenv.Directory)
	if _, err := os.Stat(contextPath); os.IsNotExist(err) {
		require.NoError(t, os.Mkdir(contextPath, 0o755), "project environment directory must be created")
	} else {
		require.NoError(t, err, "project environment directory must be inspectable")
	}
	project, err := gitproject.Discover(t.Context(), worktree)
	require.NoError(t, err, "fixture Git project must be discoverable")
	config, err := launcher.DefaultProjectConfig(project, launcher.HostEnvironment{
		HomeDir: fixture.project.hostHome, GitConfig: fixture.project.hostGit, CodexHome: fixture.project.codexHome,
	}, goSmokeImage)
	require.NoError(t, err, "fixture defaults must be serializable")
	config.Common.DependencyCaches = []projectenv.DependencyCacheConfig{
		{Kind: projectenv.DependencyCacheGoBuild, Source: buildCache},
		{Kind: projectenv.DependencyCacheGoModules, Source: moduleCache},
	}
	var encoded bytes.Buffer
	require.NoError(t, projectenv.Encode(config, &encoded), "fixture config must encode")
	require.NoError(t, os.WriteFile(filepath.Join(contextPath, projectenv.ConfigName), encoded.Bytes(), 0o600), "config must be written")
}

func cleanupGoProjectImage(t *testing.T, fixture *smokeFixture) {
	t.Helper()
	cleanupGoProjectImageAt(t, fixture, fixture.project.worktree)
}

func cleanupGoProjectImageAt(t *testing.T, fixture *smokeFixture, worktree string) {
	t.Helper()
	tag := projectenv.LocalImageName(launcher.ProjectKey(os.Getuid(), worktree))
	t.Cleanup(func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		_, _ = fixture.docker.client.ImageRemove(
			cleanupContext,
			tag,
			image.RemoveOptions{Force: true, PruneChildren: true},
		)
	})
}

func addConcurrentGoCacheWorktree(t *testing.T, fixture *smokeFixture) string {
	t.Helper()
	worktree := filepath.Join(fixture.project.root, "parallel cache worktree")
	runInDir(t, fixture.project.primary, "git", "worktree", "add", "-b", "smoke/cache-parallel", worktree)
	t.Cleanup(func() {
		command := exec.Command("git", "worktree", "remove", "--force", worktree)
		command.Dir = fixture.project.primary
		_ = command.Run()
	})
	for _, name := range []string{"go.mod", "main.go"} {
		data, err := os.ReadFile(filepath.Join(fixture.project.worktree, name))
		require.NoError(t, err, "source worktree file %q must be readable", name)
		require.NoError(t, os.WriteFile(filepath.Join(worktree, name), data, 0o600), "parallel worktree file %q must be written", name)
	}
	for _, name := range []string{projectenv.DockerfileName} {
		data, err := os.ReadFile(filepath.Join(fixture.project.worktree, projectenv.Directory, name))
		require.NoError(t, err, "source environment file %q must be readable", name)
		target := filepath.Join(worktree, projectenv.Directory, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755), "parallel environment directory must be created")
		require.NoError(t, os.WriteFile(target, data, 0o600), "parallel environment file %q must be written", name)
	}
	return worktree
}

func waitForManagedContainerRemoval(t *testing.T, fixture *smokeFixture, name string) {
	t.Helper()
	deadline := time.Now().Add(idleRemovalTimeout)
	for time.Now().Before(deadline) {
		_, err := fixture.docker.client.ContainerInspect(fixture.docker.ctx, name)
		if client.IsErrNotFound(err) {
			return
		}
		require.NoError(t, err, "parallel managed session must remain inspectable while awaiting cleanup")
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("parallel managed container %q was not removed after idle timeout", name)
}

func writeGoCacheClient(t *testing.T, directory string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(directory, "go.mod"), []byte(`module example.test/go-cache-client

go 1.23

require example.test/cachedep v1.0.0
`), 0o600), "Go cache client module must be written")
	require.NoError(t, os.WriteFile(filepath.Join(directory, "main.go"), []byte(`package main

import (
	"fmt"

	"example.test/cachedep"
)

func main() { fmt.Println(cachedep.Message) }
`), 0o600), "Go cache client source must be written")
}

func writeGoModuleProxy(t *testing.T, directory string) string {
	t.Helper()
	const modulePath = "example.test/cachedep"
	const version = "v1.0.0"
	proxy := filepath.Join(directory, "go-module-proxy")
	versionDirectory := filepath.Join(proxy, modulePath, "@v")
	require.NoError(t, os.MkdirAll(versionDirectory, 0o755), "local Go module proxy must be created")
	require.NoError(t, os.WriteFile(filepath.Join(versionDirectory, "list"), []byte(version+"\n"), 0o600), "proxy version list must be written")
	require.NoError(t, os.WriteFile(filepath.Join(versionDirectory, version+".info"), []byte(`{"Version":"v1.0.0","Time":"2026-07-20T00:00:00Z"}`), 0o600), "proxy metadata must be written")
	module := []byte("module " + modulePath + "\n\ngo 1.23\n")
	require.NoError(t, os.WriteFile(filepath.Join(versionDirectory, version+".mod"), module, 0o600), "proxy module file must be written")
	archivePath := filepath.Join(versionDirectory, version+".zip")
	archive, err := os.Create(archivePath)
	require.NoError(t, err, "proxy archive must be created")
	zipWriter := zip.NewWriter(archive)
	for _, entry := range []struct {
		name string
		data []byte
	}{
		{name: modulePath + "@" + version + "/go.mod", data: module},
		{name: modulePath + "@" + version + "/cachedep.go", data: []byte("package cachedep\n\nconst Message = \"cached dependency\"\n")},
	} {
		file, err := zipWriter.Create(entry.name)
		require.NoError(t, err, "proxy archive entry %q must be created", entry.name)
		_, err = file.Write(entry.data)
		require.NoError(t, err, "proxy archive entry %q must be written", entry.name)
	}
	require.NoError(t, zipWriter.Close(), "proxy archive must close")
	require.NoError(t, archive.Close(), "proxy archive file must close")
	return proxy
}

func runGoWithEnvironment(t *testing.T, directory string, environment []string, args ...string) {
	t.Helper()
	command := exec.Command("go", args...)
	command.Dir = directory
	command.Env = append(os.Environ(), environment...)
	output, err := command.CombinedOutput()
	require.NoError(t, err, "go %s must succeed: %s", strings.Join(args, " "), output)
}

func makeTestCacheRemovable(t *testing.T, paths ...string) {
	t.Helper()
	t.Cleanup(func() {
		for _, root := range paths {
			_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
				if err != nil || info == nil {
					return err
				}
				if info.IsDir() {
					return os.Chmod(path, 0o755)
				}
				return os.Chmod(path, 0o600)
			})
		}
	})
}
