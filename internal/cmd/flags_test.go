package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestFlagsHelpIsOffline(t *testing.T) {
	for _, mode := range []string{"", "show", "plan", "apply"} {
		t.Run(mode, func(t *testing.T) {
			root := newRootCmd()
			var stdout, stderr bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(&stderr)
			args := []string{"flags"}
			if mode != "" {
				args = append(args, mode)
			}
			root.SetArgs(append(args, "--help"))
			if err := root.Execute(); err != nil {
				t.Fatalf("offline flags help failed: %v", err)
			}
			for _, text := range []string{"application-wide", "cascade", "atomic", "authority"} {
				if !strings.Contains(stdout.String(), text) {
					t.Errorf("help missing %q: %s", text, stdout.String())
				}
			}
			if mode == "" {
				for _, name := range []string{"show", "plan", "apply"} {
					if !strings.Contains(stdout.String(), name) {
						t.Errorf("missing command %q", name)
					}
				}
			} else if !strings.Contains(stdout.String(), "--profile") {
				t.Error("missing required profile flag")
			}
			if stderr.Len() != 0 {
				t.Fatalf("help emitted diagnostics: %q", stderr.String())
			}
		})
	}
}
