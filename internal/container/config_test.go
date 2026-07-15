package container

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConfigFromEnvironment(t *testing.T) {
	environment := validEnvironment()
	environment["CODEX_SAFE_DOCKER_READY_TIMEOUT"] = "17"
	config, err := ConfigFromEnvironment(mapLookup(environment))
	require.NoError(t, err, "valid host environment must parse")
	require.Equal(t, 1000, config.HostUID, "host UID must be parsed")
	require.Equal(t, 1001, config.HostGID, "host GID must be parsed")
	require.Equal(t, "alex", config.HostUser, "host username must be parsed")
	require.Equal(t, "developers", config.HostGroup, "host group must be parsed")
	require.Equal(t, "/home/alex", config.HostHome, "host home must be parsed")
	require.Equal(t, 17*time.Second, config.DockerReadyTimeout, "daemon readiness timeout must be configurable")
	require.Equal(t, defaultDockerShutdownTimeout, config.DockerShutdownTimeout, "shutdown timeout must use its default")
}

func TestConfigFromEnvironmentRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "negative uid", key: "CODEX_SAFE_HOST_UID", value: "-1"},
		{name: "invalid gid", key: "CODEX_SAFE_HOST_GID", value: "gid"},
		{name: "invalid user", key: "CODEX_SAFE_HOST_USER", value: "Alex Smith"},
		{name: "invalid group", key: "CODEX_SAFE_HOST_GROUP", value: "1000"},
		{name: "relative home", key: "CODEX_SAFE_HOST_HOME", value: "home/alex"},
		{name: "root home", key: "CODEX_SAFE_HOST_HOME", value: "/"},
		{name: "unclean home", key: "CODEX_SAFE_HOST_HOME", value: "/home/../root"},
		{name: "comma home", key: "CODEX_SAFE_HOST_HOME", value: "/home/alex,bad"},
		{name: "zero timeout", key: "CODEX_SAFE_DOCKER_READY_TIMEOUT", value: "0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			environment := validEnvironment()
			environment[test.key] = test.value
			_, err := ConfigFromEnvironment(mapLookup(environment))
			require.Error(t, err, "invalid environment value must be rejected")
		})
	}
}

func TestConfigFromEnvironmentRequiresEveryIdentityValue(t *testing.T) {
	for _, name := range []string{
		"CODEX_SAFE_HOST_UID",
		"CODEX_SAFE_HOST_GID",
		"CODEX_SAFE_HOST_USER",
		"CODEX_SAFE_HOST_GROUP",
		"CODEX_SAFE_HOST_HOME",
	} {
		t.Run(name, func(t *testing.T) {
			environment := validEnvironment()
			delete(environment, name)
			_, err := ConfigFromEnvironment(mapLookup(environment))
			require.Error(t, err, "missing environment value must be rejected")
		})
	}
}

func validEnvironment() map[string]string {
	return map[string]string{
		"CODEX_SAFE_HOST_UID":   "1000",
		"CODEX_SAFE_HOST_GID":   "1001",
		"CODEX_SAFE_HOST_USER":  "alex",
		"CODEX_SAFE_HOST_GROUP": "developers",
		"CODEX_SAFE_HOST_HOME":  "/home/alex",
	}
}

func mapLookup(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, found := values[name]
		return value, found
	}
}

func testConfig(t *testing.T) Config {
	t.Helper()
	config, err := ConfigFromEnvironment(mapLookup(validEnvironment()))
	require.NoError(t, err, "test environment must be valid")
	return config
}
