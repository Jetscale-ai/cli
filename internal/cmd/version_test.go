package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Jetscale-ai/cli/internal/buildinfo"
	"gopkg.in/yaml.v3"
)

func TestChannelVersionAndHelpOffline(t *testing.T) {
	channel, args := buildinfo.Channel, os.Args
	backend, schema, api := buildinfo.BackendSHA, buildinfo.SchemaSHA256, buildinfo.APIVersion
	t.Cleanup(func() {
		buildinfo.Channel, os.Args = channel, args
		buildinfo.BackendSHA, buildinfo.SchemaSHA256, buildinfo.APIVersion = backend, schema, api
	})
	buildinfo.BackendSHA, buildinfo.SchemaSHA256, buildinfo.APIVersion = "abc123", "def456", "2.0"
	for _, channel := range []string{"live", "local", "unstable"} {
		for _, arg := range []string{"version", "--help"} {
			t.Run(channel+"/"+arg, func(t *testing.T) {
				buildinfo.Channel = channel
				os.Args = []string{"/sandbox/" + buildinfo.BinaryName()}
				t.Setenv("NO_COLOR", "1")
				t.Setenv("TERM", "dumb")
				// Invalid config is deliberate: version/help must never load it.
				t.Setenv(buildinfo.EnvVar("CONFIG_DIR"), "/nonexistent/offline/config")
				t.Setenv("JETSCALE_API_URL", "https://must-not-be-contacted.invalid")
				root := newRootCmd()
				var out, stderr bytes.Buffer
				root.SetOut(&out)
				root.SetErr(&stderr)
				root.SetArgs([]string{arg})
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				text := out.String()
				if stderr.Len() != 0 || strings.ContainsAny(text, "\x1b\r") {
					t.Fatalf("unexpected stderr/terminal controls: %q / %q", text, stderr.String())
				}
				if !strings.Contains(text, buildinfo.BinaryName()+" ") {
					t.Fatalf("binary name missing: %s", text)
				}
				if arg == "version" {
					for _, value := range []string{"channel: " + channel, "backend SHA: abc123", "schema SHA256: def456", "API version: 2.0", "source SHA256:", "source dirty:", "commit:", "built:"} {
						if !strings.Contains(text, value) {
							t.Errorf("missing %q in %q", value, text)
						}
					}
				} else if !strings.Contains(text, buildinfo.DefaultURL()) {
					t.Fatal("help default endpoint mismatch")
				}
				if channel != "live" && strings.Contains(text, "  jetscale ") {
					t.Fatal("live binary in command example")
				}
			})
		}
	}
}

func TestRenamedExecutableHelp(t *testing.T) {
	args := os.Args
	os.Args = []string{"/sandbox/my-local-cli"}
	t.Cleanup(func() { os.Args = args })
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"config", "set", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "my-local-cli config set") || strings.Contains(out.String(), "  jetscale config") {
		t.Fatal(out.String())
	}
}

type failedVersionWriter struct{}

func (failedVersionWriter) Write([]byte) (int, error) { return 0, errors.New("output closed") }

func TestVersionOutputFailure(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			cmd := newRootCmd()
			cmd.SetOut(failedVersionWriter{})
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs([]string{"version", "-o", format})
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "output closed") {
				t.Fatalf("output failure must propagate, got %v", err)
			}
		})
	}
}

func TestVersionStructuredProvenance(t *testing.T) {
	for _, channel := range []string{"live", "local", "unstable"} {
		for _, dirty := range []string{"true", "false", "unknown", ""} {
			for _, format := range []string{"json", "yaml"} {
				t.Run(channel+"/"+dirty+"/"+format, func(t *testing.T) {
					setVersionFixture(t, channel)
					buildinfo.SourceDirty = dirty
					if dirty == "" {
						buildinfo.BackendSHA, buildinfo.SchemaSHA256, buildinfo.APIVersion, buildinfo.SourceSHA256 = "", "", "", ""
					}
					root := newRootCmd()
					var out, stderr bytes.Buffer
					root.SetOut(&out)
					root.SetErr(&stderr)
					root.SetArgs([]string{"version", "-o", format})
					if err := root.Execute(); err != nil {
						t.Fatal(err)
					}
					if stderr.Len() != 0 || strings.ContainsAny(out.String(), "\x1b\r") {
						t.Fatalf("unexpected stderr or terminal controls: %q / %q", out.String(), stderr.String())
					}
					var got map[string]string
					var err error
					if format == "json" {
						err = json.Unmarshal(out.Bytes(), &got)
					} else {
						err = yaml.Unmarshal(out.Bytes(), &got)
					}
					if err != nil {
						t.Fatalf("invalid structured output: %v", err)
					}
					want := map[string]string{
						"binary": "renamed-cli", "version": "1.2.3", "commit": "cli-commit", "date": "2026-09-14T00:00:00Z",
						"channel": channel, "backend_sha": "backend-commit", "schema_sha256": "schema-digest",
						"api_version": "2.0", "source_sha256": "source-digest", "source_dirty": dirty,
					}
					if dirty == "" {
						for _, key := range []string{"backend_sha", "schema_sha256", "api_version", "source_sha256", "source_dirty"} {
							want[key] = "unknown"
						}
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("provenance = %#v, want %#v", got, want)
					}
				})
			}
		}
	}
}

func TestVersionTextCompatibility(t *testing.T) {
	for _, channel := range []string{"live", "local", "unstable"} {
		for _, args := range [][]string{{"version"}, {"version", "-o", "table"}} {
			t.Run(channel+"/"+strings.Join(args, " "), func(t *testing.T) {
				setVersionFixture(t, channel)
				root := newRootCmd()
				var out, stderr bytes.Buffer
				root.SetOut(&out)
				root.SetErr(&stderr)
				root.SetArgs(args)
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				want := fmt.Sprintf("renamed-cli 1.2.3 (commit: cli-commit, built: 2026-09-14T00:00:00Z)\nchannel: %s\nbackend SHA: backend-commit\nschema SHA256: schema-digest\nAPI version: 2.0\nsource SHA256: source-digest\nsource dirty: false\n", channel)
				if out.String() != want || stderr.Len() != 0 {
					t.Fatalf("text output changed: %q / stderr: %q", out.String(), stderr.String())
				}
			})
		}
	}
}

func TestVersionInvalidOutputFormat(t *testing.T) {
	root := newRootCmd()
	var out, stderr bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&stderr)
	root.SetArgs([]string{"version", "-o", "invalid"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "unknown output format") {
		t.Fatalf("expected output format error, got %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("invalid format wrote to stdout: %q", out.String())
	}
}

func setVersionFixture(t *testing.T, channel string) {
	t.Helper()
	oldChannel, oldArgs := buildinfo.Channel, os.Args
	oldVersion, oldCommit, oldDate := version, commit, date
	backend, schema, api := buildinfo.BackendSHA, buildinfo.SchemaSHA256, buildinfo.APIVersion
	source, dirty := buildinfo.SourceSHA256, buildinfo.SourceDirty
	t.Cleanup(func() {
		buildinfo.Channel, os.Args = oldChannel, oldArgs
		version, commit, date = oldVersion, oldCommit, oldDate
		buildinfo.BackendSHA, buildinfo.SchemaSHA256, buildinfo.APIVersion = backend, schema, api
		buildinfo.SourceSHA256, buildinfo.SourceDirty = source, dirty
	})
	buildinfo.Channel, os.Args = channel, []string{"/sandbox/renamed-cli"}
	version, commit, date = "1.2.3", "cli-commit", "2026-09-14T00:00:00Z"
	buildinfo.BackendSHA, buildinfo.SchemaSHA256, buildinfo.APIVersion = "backend-commit", "schema-digest", "2.0"
	buildinfo.SourceSHA256, buildinfo.SourceDirty = "source-digest", "false"
	t.Setenv("NO_COLOR", "1")
	t.Setenv("TERM", "dumb")
	// A file cannot be a config directory: version must not try to load config.
	configFile := t.TempDir() + "/not-a-directory"
	if err := os.WriteFile(configFile, []byte("invalid config"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(buildinfo.EnvVar("CONFIG_DIR"), configFile)
	t.Setenv(buildinfo.EnvVar("API_URL"), ":invalid-url")
}

func TestVersionEmptyProvenance(t *testing.T) {
	if provenance("") != "unknown" {
		t.Fatal("empty CI metadata must be unknown")
	}
}
