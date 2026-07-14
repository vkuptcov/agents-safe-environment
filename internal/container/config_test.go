package container

import (
	"testing"
	"time"
)

func TestConfigFromEnvironment(t *testing.T) {
	environment := validEnvironment()
	environment["CODEX_SAFE_DOCKER_READY_TIMEOUT"] = "17"
	config, err := ConfigFromEnvironment(mapLookup(environment))
	if err != nil {
		t.Fatalf("ConfigFromEnvironment() error = %v", err)
	}
	if config.HostUID != 1000 || config.HostGID != 1001 {
		t.Fatalf("identity = %d:%d, want 1000:1001", config.HostUID, config.HostGID)
	}
	if config.HostUser != "alex" || config.HostGroup != "developers" {
		t.Fatalf("account = %q:%q", config.HostUser, config.HostGroup)
	}
	if config.HostHome != "/home/alex" {
		t.Fatalf("home = %q", config.HostHome)
	}
	if config.DockerReadyTimeout != 17*time.Second {
		t.Fatalf("ready timeout = %s", config.DockerReadyTimeout)
	}
	if config.DockerShutdownTimeout != defaultDockerShutdownTimeout {
		t.Fatalf("shutdown timeout = %s", config.DockerShutdownTimeout)
	}
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
			if _, err := ConfigFromEnvironment(mapLookup(environment)); err == nil {
				t.Fatalf("ConfigFromEnvironment() accepted %s=%q", test.key, test.value)
			}
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
			if _, err := ConfigFromEnvironment(mapLookup(environment)); err == nil {
				t.Fatalf("ConfigFromEnvironment() accepted missing %s", name)
			}
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
	if err != nil {
		t.Fatalf("create test config: %v", err)
	}
	return config
}
