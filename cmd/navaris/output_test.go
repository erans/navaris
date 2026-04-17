package main

import (
	"bytes"
	"io"
	"os"
	"testing"
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

	fn()
	w.Close()
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
