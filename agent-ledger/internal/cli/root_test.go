package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func newTestStreams() (IOStreams, *bytes.Buffer, *bytes.Buffer) {
	out := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	return IOStreams{In: bytes.NewReader(nil), Out: out, Err: errBuf}, out, errBuf
}

func TestRootHelpListsAllPhase1Commands(t *testing.T) {
	streams, out, _ := newTestStreams()
	code := Execute(streams, []string{"--help"})
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d", code, ExitOK)
	}
	got := out.String()
	for _, name := range Phase1Commands() {
		if !strings.Contains(got, name) {
			t.Errorf("--help output missing command %q", name)
		}
	}
}

func TestPhase1CommandCount(t *testing.T) {
	if got, want := len(Phase1Commands()), 15; got != want {
		t.Fatalf("Phase1Commands count = %d, want %d", got, want)
	}
}

func TestRootVersionIsNonEmpty(t *testing.T) {
	streams, out, _ := newTestStreams()
	code := Execute(streams, []string{"--version"})
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d", code, ExitOK)
	}
	if v := strings.TrimSpace(out.String()); v == "" {
		t.Fatal("--version output is empty")
	}
}

func TestStubCommandsReturnExit3(t *testing.T) {
	// init, doctor, and migrate are now wired; everything else is still stubbed.
	wired := map[string]bool{"init": true, "doctor": true, "migrate": true}
	for _, name := range Phase1Commands() {
		if wired[name] {
			continue
		}
		t.Run(name, func(t *testing.T) {
			streams, _, errBuf := newTestStreams()
			code := Execute(streams, []string{name})
			if code != ExitNotImplemented {
				t.Fatalf("%s: exit code = %d, want %d", name, code, ExitNotImplemented)
			}
			if !strings.Contains(errBuf.String(), "not implemented") {
				t.Errorf("%s: stderr lacks not-implemented message: %q", name, errBuf.String())
			}
		})
	}
}

func TestStubCommandJSONEnvelope(t *testing.T) {
	streams, _, errBuf := newTestStreams()
	code := Execute(streams, []string{"claim", "--json"})
	if code != ExitNotImplemented {
		t.Fatalf("exit code = %d, want %d", code, ExitNotImplemented)
	}
	var env map[string]any
	if err := json.Unmarshal(errBuf.Bytes(), &env); err != nil {
		t.Fatalf("stderr is not JSON: %v\n%s", err, errBuf.String())
	}
	if env["status"] != "error" {
		t.Errorf("status = %v", env["status"])
	}
	if env["code"] != "not_implemented" {
		t.Errorf("code = %v", env["code"])
	}
}

func TestUnknownCommandIsUsageError(t *testing.T) {
	streams, _, _ := newTestStreams()
	code := Execute(streams, []string{"definitely-not-a-real-command"})
	if code == ExitOK {
		t.Fatal("unknown command should not return ExitOK")
	}
}
