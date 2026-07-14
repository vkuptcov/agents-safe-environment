package container

import (
	"context"
	"fmt"
	"testing"
)

func TestConfigureHostAccountReusesMatchingAccount(t *testing.T) {
	config := testConfig(t)
	commands := &fakeCommandRunner{responses: map[string]commandResponse{
		commandKey("getent", "group", "1001"):       {output: "developers:x:1001:\n"},
		commandKey("getent", "group", "developers"): {output: "developers:x:1001:\n"},
		commandKey("getent", "passwd", "1000"): {
			output: "alex:x:1000:1001::/home/alex:/bin/bash\n",
		},
		commandKey("getent", "passwd", "alex"): {
			output: "alex:x:1000:1001::/home/alex:/bin/bash\n",
		},
		commandKey("usermod", "--gid", "1001", "--home", "/home/alex", "alex"): {},
	}}
	if err := configureHostAccount(context.Background(), config, commands); err != nil {
		t.Fatalf("configureHostAccount() error = %v", err)
	}
}

func TestConfigureHostAccountCreatesMissingEntries(t *testing.T) {
	config := testConfig(t)
	commands := &fakeCommandRunner{responses: map[string]commandResponse{
		commandKey("getent", "group", "1001"):                 {err: fakeCommandError{code: 2}},
		commandKey("getent", "group", "developers"):           {err: fakeCommandError{code: 2}},
		commandKey("groupadd", "--gid", "1001", "developers"): {},
		commandKey("getent", "passwd", "1000"):                {err: fakeCommandError{code: 2}},
		commandKey("getent", "passwd", "alex"):                {err: fakeCommandError{code: 2}},
		commandKey(
			"useradd",
			"--uid", "1000",
			"--gid", "1001",
			"--home-dir", "/home/alex",
			"--no-create-home",
			"--shell", "/bin/bash",
			"alex",
		): {},
	}}
	if err := configureHostAccount(context.Background(), config, commands); err != nil {
		t.Fatalf("configureHostAccount() error = %v", err)
	}
}

func TestConfigureHostAccountRenamesImageEntries(t *testing.T) {
	config := testConfig(t)
	commands := &fakeCommandRunner{responses: map[string]commandResponse{
		commandKey("getent", "group", "1001"):                        {output: "ubuntu:x:1001:\n"},
		commandKey("getent", "group", "developers"):                  {err: fakeCommandError{code: 2}},
		commandKey("groupmod", "--new-name", "developers", "ubuntu"): {},
		commandKey("getent", "passwd", "1000"): {
			output: "ubuntu:x:1000:1001::/home/ubuntu:/bin/bash\n",
		},
		commandKey("getent", "passwd", "alex"):                                 {err: fakeCommandError{code: 2}},
		commandKey("usermod", "--login", "alex", "ubuntu"):                     {},
		commandKey("usermod", "--gid", "1001", "--home", "/home/alex", "alex"): {},
	}}
	if err := configureHostAccount(context.Background(), config, commands); err != nil {
		t.Fatalf("configureHostAccount() error = %v", err)
	}
}

func TestConfigureHostAccountRejectsNameConflicts(t *testing.T) {
	tests := []struct {
		name      string
		responses map[string]commandResponse
	}{
		{
			name: "group name uses another gid",
			responses: map[string]commandResponse{
				commandKey("getent", "group", "1001"):       {err: fakeCommandError{code: 2}},
				commandKey("getent", "group", "developers"): {output: "developers:x:2000:\n"},
			},
		},
		{
			name: "user name uses another uid",
			responses: map[string]commandResponse{
				commandKey("getent", "group", "1001"):       {output: "developers:x:1001:\n"},
				commandKey("getent", "group", "developers"): {output: "developers:x:1001:\n"},
				commandKey("getent", "passwd", "1000"):      {err: fakeCommandError{code: 2}},
				commandKey("getent", "passwd", "alex"):      {output: "alex:x:2000:1001::/home/alex:/bin/bash\n"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := configureHostAccount(
				context.Background(),
				testConfig(t),
				&fakeCommandRunner{responses: test.responses},
			); err == nil {
				t.Fatal("configureHostAccount() accepted a conflicting account")
			}
		})
	}
}

type commandResponse struct {
	output string
	err    error
}

type fakeCommandRunner struct {
	responses map[string]commandResponse
	calls     []string
}

func (runner *fakeCommandRunner) CombinedOutput(
	_ context.Context,
	name string,
	arguments ...string,
) ([]byte, error) {
	key := commandKey(name, arguments...)
	runner.calls = append(runner.calls, key)
	response, found := runner.responses[key]
	if !found {
		return nil, fmt.Errorf("unexpected command %s", key)
	}
	return []byte(response.output), response.err
}

type fakeCommandError struct {
	code int
}

func (err fakeCommandError) Error() string {
	return fmt.Sprintf("exit status %d", err.code)
}

func (err fakeCommandError) ExitCode() int {
	return err.code
}

func commandKey(name string, arguments ...string) string {
	return fmt.Sprintf("%s|%q", name, arguments)
}

var _ error = fakeCommandError{}
