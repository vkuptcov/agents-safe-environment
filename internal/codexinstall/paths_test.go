package codexinstall

import "testing"

func TestReleasePathAndDirName(t *testing.T) {
	t.Parallel()
	target := Target{Architecture: ArchitectureAMD64}
	if got, want := ReleaseDirName("0.150.0", target), "0.150.0-linux-amd64"; got != want {
		t.Fatalf("ReleaseDirName() = %q, want %q", got, want)
	}
	if got, want := ReleasePath("0.150.0", target), "releases/0.150.0-linux-amd64"; got != want {
		t.Fatalf("ReleasePath() = %q, want %q", got, want)
	}
}

func TestWithinStoreAcceptsContainedPaths(t *testing.T) {
	t.Parallel()
	cases := []struct{ candidate, want string }{
		{CurrentEntryName, "/store/current"},
		{ReleasePath("0.150.0", Target{Architecture: ArchitectureAMD64}), "/store/releases/0.150.0-linux-amd64"},
		{"releases/./0.150.0-linux-amd64", "/store/releases/0.150.0-linux-amd64"},
	}
	for _, testCase := range cases {
		got, err := WithinStore("/store", testCase.candidate)
		if err != nil {
			t.Fatalf("WithinStore(%q) error = %v", testCase.candidate, err)
		}
		if got != testCase.want {
			t.Fatalf("WithinStore(%q) = %q, want %q", testCase.candidate, got, testCase.want)
		}
	}
}

func TestWithinStoreRejectsEscapingPaths(t *testing.T) {
	t.Parallel()
	for _, candidate := range []string{
		"../etc/passwd",
		"releases/../../etc/passwd",
		"releases/../..",
		"..",
	} {
		if _, err := WithinStore("/store", candidate); err == nil {
			t.Fatalf("WithinStore(%q) must reject a path escaping the store", candidate)
		}
	}
}

func TestWithinStoreRejectsEmptyInputs(t *testing.T) {
	t.Parallel()
	if _, err := WithinStore("", "current"); err == nil {
		t.Fatal("WithinStore() must reject an empty store root")
	}
	if _, err := WithinStore("/store", ""); err == nil {
		t.Fatal("WithinStore() must reject an empty candidate")
	}
}
