package codexinstall

import "testing"

func TestParseTargetAcceptsSupportedLinuxTargets(t *testing.T) {
	t.Parallel()
	for _, architecture := range []string{ArchitectureAMD64, ArchitectureARM64} {
		target, err := ParseTarget(LinuxOS, architecture)
		if err != nil {
			t.Fatalf("ParseTarget(linux, %s) error = %v", architecture, err)
		}
		if target.Architecture != architecture {
			t.Fatalf("ParseTarget(linux, %s) = %#v", architecture, target)
		}
		want := "linux-" + architecture
		if target.String() != want {
			t.Fatalf("target.String() = %q, want %q", target.String(), want)
		}
	}
}

func TestParseTargetRejectsNonLinuxDaemon(t *testing.T) {
	t.Parallel()
	for _, daemonOS := range []string{"darwin", "windows", ""} {
		if _, err := ParseTarget(daemonOS, ArchitectureAMD64); err == nil {
			t.Fatalf("ParseTarget(%q, amd64) must reject a non-Linux daemon", daemonOS)
		}
	}
}

func TestParseTargetRejectsUnsupportedArchitecture(t *testing.T) {
	t.Parallel()
	for _, architecture := range []string{"386", "arm", "riscv64", ""} {
		if _, err := ParseTarget(LinuxOS, architecture); err == nil {
			t.Fatalf("ParseTarget(linux, %q) must reject an unsupported architecture", architecture)
		}
	}
}
