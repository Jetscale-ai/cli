package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Jetscale-ai/cli/internal/buildinfo"
	"github.com/spf13/cobra"
)

const flagsProfileFixture = `{"version":1,"company":{"name":"Test Company","slug":"test-company"},"flags":{"root":{"AGENTS.EXPLORER":true},"company":{},"business_unit":{}}}`

func flagsCommandTest(t *testing.T, channel string) string {
	t.Helper()
	previous := buildinfo.Channel
	buildinfo.Channel = channel
	t.Cleanup(func() { buildinfo.Channel = previous })
	dir := t.TempDir()
	for _, key := range []string{"CONFIG_DIR", "API_URL", "INSTANCE", "TOKEN"} {
		value := ""
		if key == "CONFIG_DIR" {
			value = dir
		}
		t.Setenv(buildinfo.EnvVar(key), value)
	}
	return dir
}

func flagsProfileFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

type flagsCountingInput struct{ reads int }

func (r *flagsCountingInput) Read([]byte) (int, error) {
	r.reads++
	return 0, errors.New("sensitive-sentinel: password must not be read")
}

func TestFlagsValidateOnlyOffline(t *testing.T) {
	for _, mode := range []string{"show", "plan"} {
		for _, format := range []string{"json", "yaml", "table"} {
			t.Run(mode+"/"+format, func(t *testing.T) {
				dir := flagsCommandTest(t, "unstable")
				root := newRootCmd()
				var stdout, stderr bytes.Buffer
				input := &flagsCountingInput{}
				root.SetIn(input)
				root.SetOut(&stdout)
				root.SetErr(&stderr)
				root.SetArgs([]string{"flags", mode, "--profile", flagsProfileFile(t, flagsProfileFixture), "--validate-only", "--output", format})
				if err := root.Execute(); err != nil {
					t.Fatalf("offline validation: %v", err)
				}
				if format == "json" {
					var value map[string]any
					if err := json.Unmarshal(stdout.Bytes(), &value); err != nil || value["accepted"] != true || value["validate_only"] != true || value["target"] != buildinfo.UnstableURL || value["mode"] != mode || value["root_application_wide"] != true {
						t.Fatalf("invalid validation result: %s %v", stdout.String(), err)
					}
				}
				if !strings.Contains(stdout.String(), buildinfo.UnstableURL) || strings.Contains(stdout.String(), "Test Company") || stderr.Len() != 0 || input.reads != 0 {
					t.Fatalf("offline streams/auth: stdout=%q stderr=%q reads=%d", stdout.String(), stderr.String(), input.reads)
				}
				entries, err := os.ReadDir(dir)
				if err != nil || len(entries) != 0 {
					t.Fatalf("offline mode wrote state: %v %v", entries, err)
				}
			})
		}
	}
}

func TestFlagsRejectsProfileBeforeAuthentication(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		args       []string
	}{
		{"missing", "", nil},
		{"malformed", `{"version":`, nil},
		{"null", `null`, nil},
		{"array", `[]`, nil},
		{"trailing", flagsProfileFixture + `{}`, nil},
		{"garbage", flagsProfileFixture + `sensitive-sentinel`, nil},
		{"unknown-top", strings.Replace(flagsProfileFixture, `"version":`, `"sensitive-sentinel":true,"version":`, 1), nil},
		{"unknown-nested", strings.Replace(flagsProfileFixture, `"name":`, `"sensitive-sentinel":true,"name":`, 1), nil},
		{"unknown-layer", strings.Replace(flagsProfileFixture, `"root":`, `"sensitive-sentinel":{},"root":`, 1), nil},
		{"duplicate-top", strings.Replace(flagsProfileFixture, `"version":`, `"version":1,"version":`, 1), nil},
		{"duplicate-nested", strings.Replace(flagsProfileFixture, `"name":`, `"name":"sensitive-sentinel","name":`, 1), nil},
		{"duplicate-key", strings.Replace(flagsProfileFixture, `"AGENTS.EXPLORER":true`, `"AGENTS.EXPLORER":true,"AGENTS.EXPLORER":true`, 1), nil},
		{"escaped-duplicate", strings.Replace(flagsProfileFixture, `"AGENTS.EXPLORER":true`, `"AGENTS.EXPLORER":true,"AGENTS.\u0045XPLORER":true`, 1), nil},
		{"version", strings.Replace(flagsProfileFixture, `"version":1`, `"version":2`, 1), nil},
		{"field-case", strings.Replace(flagsProfileFixture, `"version":1`, `"Version":1`, 1), nil},
		{"unicode-case-alias", strings.Replace(flagsProfileFixture, `"version":1`, `"ver\u017fion":1`, 1), nil},
		{"case-alias-duplicate", strings.Replace(flagsProfileFixture, `"version":1`, `"version":1,"Version":1`, 1), nil},
		{"disable", strings.Replace(flagsProfileFixture, `"AGENTS.EXPLORER":true`, `"AGENTS.EXPLORER":false`, 1), nil},
		{"null-value", strings.Replace(flagsProfileFixture, `"AGENTS.EXPLORER":true`, `"AGENTS.EXPLORER":null`, 1), nil},
		{"unknown-key", strings.Replace(flagsProfileFixture, `AGENTS.EXPLORER`, `sensitive-sentinel`, 1), nil},
		{"invalid-type", strings.Replace(flagsProfileFixture, `"AGENTS.EXPLORER":true`, `"AGENTS.EXPLORER":"sensitive-sentinel"`, 1), nil},
		{"cascade", strings.Replace(flagsProfileFixture, `"version":`, `"company_cascade":"sensitive-sentinel","version":`, 1), nil},
		{"output", flagsProfileFixture, []string{"--output", "sensitive-sentinel"}},
		{"account-override", flagsProfileFixture, []string{"--account", "sensitive-sentinel"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := flagsCommandTest(t, "unstable")
			root := newRootCmd()
			var stdout, stderr bytes.Buffer
			input := &flagsCountingInput{}
			root.SetIn(input)
			root.SetOut(&stdout)
			root.SetErr(&stderr)
			args := []string{"flags", "plan", "--login", "synthetic@example.invalid", "--password-stdin"}
			if tc.body != "" {
				args = append(args, "--profile", flagsProfileFile(t, tc.body))
			}
			root.SetArgs(append(args, tc.args...))
			err := root.Execute()
			if err == nil || strings.Contains(err.Error(), "TOKEN") || strings.Contains(err.Error(), "sensitive-sentinel") {
				t.Fatalf("preflight accepted input/reached auth/leaked raw data: %v", err)
			}
			if input.reads != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("preflight reached auth/output: reads=%d stdout=%q stderr=%q", input.reads, stdout.String(), stderr.String())
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("preflight wrote files: %v %v", entries, err)
			}
		})
	}
}

func TestFlagsValidateOnlyRejectsApplyAndAuthFlags(t *testing.T) {
	for _, mode := range []string{"show", "plan", "apply"} {
		for _, extra := range [][]string{nil, {"--login="}, {"--login", "synthetic@example.invalid"}, {"--password-stdin"}, {"--password-stdin=false"}, {"--credentials-file", "not-read"}, {"--credentials-file="}, {"--ca-file", "not-read"}, {"--ca-file="}} {
			if mode != "apply" && extra == nil {
				continue
			}
			t.Run(mode+strings.Join(extra, " "), func(t *testing.T) {
				flagsCommandTest(t, "unstable")
				root := newRootCmd()
				input := &flagsCountingInput{}
				root.SetIn(input)
				root.SetOut(io.Discard)
				root.SetErr(io.Discard)
				root.SetArgs(append([]string{"flags", mode, "--validate-only", "--profile", flagsProfileFile(t, flagsProfileFixture)}, extra...))
				if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "--validate-only") {
					t.Fatalf("unsafe offline combination accepted: %v", err)
				}
				if input.reads != 0 {
					t.Fatal("offline mode read password")
				}
			})
		}
	}
}

func TestFlagsProfileFileBounds(t *testing.T) {
	for _, kind := range []string{"missing", "directory", "symlink", "parent-symlink", "oversized", "device"} {
		t.Run(kind, func(t *testing.T) {
			flagsCommandTest(t, "unstable")
			path := filepath.Join(t.TempDir(), "profile.json")
			switch kind {
			case "directory":
				path = t.TempDir()
			case "symlink":
				if err := os.Symlink(flagsProfileFile(t, flagsProfileFixture), path); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			case "parent-symlink":
				actual := flagsProfileFile(t, flagsProfileFixture)
				link := filepath.Join(t.TempDir(), "linked")
				if err := os.Symlink(filepath.Dir(actual), link); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
				path = filepath.Join(link, "profile.json")
			case "oversized":
				path = flagsProfileFile(t, flagsProfileFixture+strings.Repeat(" ", (1<<20)+1))
			case "device":
				if _, err := os.Stat("/dev/null"); err != nil {
					t.Skip("no /dev/null")
				}
				path = "/dev/null"
			}
			root := newRootCmd()
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.SetArgs([]string{"flags", "show", "--profile", path, "--validate-only"})
			if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "regular") {
				t.Fatalf("non-regular/unbounded profile accepted: %v", err)
			}
		})
	}
}

func TestFlagsGenericOfflineTarget(t *testing.T) {
	for _, channel := range []string{"live", "local", "unstable"} {
		for _, target := range []string{"https://tenant.example.test", "https://TENANT.example.test:443/", "http://127.0.0.1:1", "http://[::1]:1", "http://localhost:1", "http://tenant.example.test", "https://tenant.example.test/api", "https://sensitive-sentinel@tenant.example.test", "https://tenant.example.test?", "https://tenant.example.test/#sensitive-sentinel", "https://tenant.example.test:99999", "https://tenant.example.test:", "auto"} {
			t.Run(channel+"/"+target, func(t *testing.T) {
				flagsCommandTest(t, channel)
				root := newRootCmd()
				var stdout bytes.Buffer
				root.SetOut(&stdout)
				root.SetErr(io.Discard)
				root.SetArgs([]string{"flags", "show", "--profile", flagsProfileFile(t, flagsProfileFixture), "--validate-only", "--api-url", target, "--output", "json"})
				err := root.Execute()
				allowed := strings.HasPrefix(target, "https://tenant.example.test") && target == "https://tenant.example.test" || target == "https://TENANT.example.test:443/" || strings.HasPrefix(target, "http://127.0.0.1:") || target == "http://[::1]:1" || target == "http://localhost:1"
				if (err == nil) != allowed {
					t.Fatalf("incorrect target acceptance: allowed=%t err=%v", allowed, err)
				}
				if err != nil && (strings.Contains(err.Error(), "sensitive-sentinel") || stdout.Len() != 0) {
					t.Fatal("invalid target leaked or emitted output")
				}
			})
		}
	}
}

func TestFlagsOfflineRejectsAutoWithoutProbe(t *testing.T) {
	dir := flagsCommandTest(t, "live")
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("default_instance: probe\ninstances:\n  probe:\n    api_url: auto\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := newRootCmd()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"flags", "show", "--profile", flagsProfileFile(t, flagsProfileFixture), "--validate-only"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "--api-url") {
		t.Fatalf("auto must fail offline with explicit-target recovery: %v", err)
	}
}

func TestFlagsDeadlineAndTargetBeforeEagerAuth(t *testing.T) {
	for _, shorter := range []bool{false, true} {
		t.Run(fmt.Sprint(shorter), func(t *testing.T) {
			flagsCommandTest(t, "unstable")
			root := newRootCmd()
			leaf, _, err := root.Find([]string{"flags", "plan"})
			if err != nil {
				t.Fatal(err)
			}
			preflight := leaf.PreRunE
			leaf.PreRunE = func(cmd *cobra.Command, args []string) error {
				if err := preflight(cmd, args); err != nil {
					return err
				}
				flagAPIURL = "http://not-contacted.invalid"
				instance, origin, err := resolveSessionTarget(cmd)
				if err != nil || instance != "--api-url" || origin != "https://tenant.example.test" {
					t.Fatalf("target not pinned: %s %s %v", instance, origin, err)
				}
				return nil
			}
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.SetArgs([]string{"flags", "plan", "--profile", flagsProfileFile(t, flagsProfileFixture), "--api-url", "https://tenant.example.test", "--login", "synthetic@example.invalid", "--password-stdin"})
			parent := context.Background()
			if shorter {
				var cancel context.CancelFunc
				parent, cancel = context.WithTimeout(parent, 10*time.Second)
				defer cancel()
			}
			if err := root.ExecuteContext(parent); err == nil || !strings.Contains(err.Error(), "TOKEN") {
				t.Fatalf("expected eager auth rejection: %v", err)
			}
			deadline, ok := leaf.Context().Deadline()
			if !ok || time.Until(deadline) > 3*time.Minute || !errors.Is(leaf.Context().Err(), context.Canceled) || parent.Err() != nil {
				t.Fatalf("pre-auth deadline not canceled correctly: %v %v", deadline, leaf.Context().Err())
			}
			if shorter {
				parentDeadline, _ := parent.Deadline()
				if !deadline.Equal(parentDeadline) {
					t.Fatal("shorter parent deadline lost")
				}
			} else if time.Until(deadline) < 2*time.Minute {
				t.Fatal("three-minute command budget not set")
			}
		})
	}
}
