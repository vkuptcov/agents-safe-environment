package dockercli

import (
	"context"
	"reflect"
	"testing"
)

func TestInspectVolumeDecodesLabels(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{output: []byte(
		`[{"Name":"codex-safe-codex-v1-1000-linux-amd64","Labels":{"codex-safe.managed":"true"}}]`,
	)}
	inspection, found, err := New("docker", runner).InspectVolume(context.Background(), "codex-safe-codex-v1-1000-linux-amd64")
	if err != nil {
		t.Fatalf("InspectVolume() error = %v", err)
	}
	if !found {
		t.Fatal("InspectVolume() found = false, want true")
	}
	if inspection.Labels["codex-safe.managed"] != "true" {
		t.Fatalf("InspectVolume() = %#v", inspection)
	}
}

func TestInspectVolumeTranslatesNotFound(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{
		output: []byte("Error response from daemon: get missing-volume: no such volume"),
		err:    fakeExitError{code: 1},
	}
	_, found, err := New("docker", runner).InspectVolume(context.Background(), "missing-volume")
	if err != nil {
		t.Fatalf("InspectVolume() error = %v", err)
	}
	if found {
		t.Fatal("InspectVolume() found = true, want false for a not-found volume")
	}
}

func TestInspectVolumeSurfacesTransportFailureDistinctFromNotFound(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{
		output: []byte("Error response from daemon: permission denied"),
		err:    fakeExitError{code: 1},
	}
	_, found, err := New("docker", runner).InspectVolume(context.Background(), "codex-store")
	if err == nil {
		t.Fatal("InspectVolume() must surface a transport failure as an error")
	}
	if found {
		t.Fatal("InspectVolume() found = true on a transport failure")
	}
}

func TestInspectVolumeRejectsMalformedSuccessfulOutput(t *testing.T) {
	t.Parallel()
	cases := [][]byte{
		[]byte("not json"),
		[]byte(`[]`),
		[]byte(`[{"Name":"other-volume","Labels":{}}]`),
		[]byte(`[{"Name":"codex-store","Labels":{}},{"Name":"codex-store","Labels":{}}]`),
	}
	for _, output := range cases {
		runner := &fakeRunner{output: output}
		if _, found, err := New("docker", runner).InspectVolume(context.Background(), "codex-store"); err == nil {
			t.Fatalf("InspectVolume() must reject malformed output %s, got found=%t", output, found)
		}
	}
}

func TestBuildVolumeCreateArgsOrdersLabelsAndName(t *testing.T) {
	t.Parallel()
	got, err := BuildVolumeCreateArgs("codex-store", []KeyValue{
		{Key: "codex-safe.managed", Value: "true"},
		{Key: "codex-safe.host-uid", Value: "1000"},
	})
	if err != nil {
		t.Fatalf("BuildVolumeCreateArgs() error = %v", err)
	}
	want := []string{
		"volume", "create",
		"--label", "codex-safe.managed=true",
		"--label", "codex-safe.host-uid=1000",
		"codex-store",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildVolumeCreateArgs() = %#v, want %#v", got, want)
	}
}

func TestBuildVolumeCreateArgsRequiresName(t *testing.T) {
	t.Parallel()
	if _, err := BuildVolumeCreateArgs("", nil); err == nil {
		t.Fatal("BuildVolumeCreateArgs() must reject an empty name")
	}
}

func TestCreateVolumeSendsNameAndLabels(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{output: []byte("codex-store\n")}
	err := New("docker", runner).CreateVolume(context.Background(), "codex-store", []KeyValue{
		{Key: "codex-safe.managed", Value: "true"},
	})
	if err != nil {
		t.Fatalf("CreateVolume() error = %v", err)
	}
	want := []string{"volume", "create", "--label", "codex-safe.managed=true", "codex-store"}
	if !reflect.DeepEqual(runner.args, want) {
		t.Fatalf("CreateVolume() argv = %#v, want %#v", runner.args, want)
	}
}
