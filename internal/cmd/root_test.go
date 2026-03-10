package cmd

import (
	"bytes"
	"testing"
)

func TestRootCommand(t *testing.T) {
	root := newRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"--help"})

	if err := root.Execute(); err != nil {
		t.Fatalf("root --help failed: %v", err)
	}

	out := buf.String()
	if len(out) == 0 {
		t.Fatal("expected help output, got empty string")
	}
}

func TestVersionCommand(t *testing.T) {
	root := newRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("version command failed: %v", err)
	}
}
