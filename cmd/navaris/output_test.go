package main

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/navaris/navaris/pkg/client"
)

// captureStdout temporarily redirects os.Stdout, runs fn, returns captured output.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = old })

	done := make(chan string)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	defer func() {
		if p := recover(); p != nil {
			_ = w.Close()
			panic(p) // re-raise so the test still fails
		}
	}()
	fn()
	_ = w.Close()
	return <-done
}

func TestPrintQuietIDs_PrintsOneIDPerLine(t *testing.T) {
	out := captureStdout(t, func() {
		printQuietIDs([]string{"sbx-1", "sbx-2", "sbx-3"})
	})
	want := "sbx-1\nsbx-2\nsbx-3\n"
	if out != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

func TestPrintQuietIDs_EmptySliceProducesNoOutput(t *testing.T) {
	out := captureStdout(t, func() {
		printQuietIDs(nil)
	})
	if out != "" {
		t.Errorf("got %q, want empty", out)
	}
}

func TestIsQuiet_DefaultFalse(t *testing.T) {
	// rootCmd was initialised in init(); read default
	if isQuiet() {
		t.Error("expected isQuiet() == false by default")
	}
}

func TestIsQuiet_TrueWhenFlagSet(t *testing.T) {
	if err := rootCmd.PersistentFlags().Set("quiet", "true"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rootCmd.PersistentFlags().Set("quiet", "false") })

	if !isQuiet() {
		t.Error("expected isQuiet() == true after --quiet=true")
	}
}

func TestResourceID(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"sandbox", &client.Sandbox{SandboxID: "sbx-1"}, "sbx-1"},
		{"session", &client.Session{SessionID: "ses-1"}, "ses-1"},
		{"snapshot", &client.Snapshot{SnapshotID: "snap-1"}, "snap-1"},
		{"image", &client.BaseImage{ImageID: "img-1"}, "img-1"},
		{"project", &client.Project{ProjectID: "prj-1"}, "prj-1"},
		{"operation", &client.Operation{OperationID: "op-1"}, "op-1"},
		{"unknown_string", "hello", ""},
		{"nil", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resourceID(tc.in); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
