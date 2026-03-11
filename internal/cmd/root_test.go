package cmd

import (
	"bytes"
	"strings"
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

func TestRootHasPersistentFlags(t *testing.T) {
	root := newRootCmd()

	for _, name := range []string{"instance", "local", "api-url", "account"} {
		if root.PersistentFlags().Lookup(name) == nil {
			t.Errorf("missing persistent flag %q", name)
		}
	}
	// -i shorthand
	if f := root.PersistentFlags().ShorthandLookup("i"); f == nil {
		t.Error("missing -i shorthand for --instance")
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

	out := buf.String()
	if !strings.Contains(out, "jetscale") {
		t.Errorf("version output missing 'jetscale': %q", out)
	}
}

func TestConfigShowDefault(t *testing.T) {
	t.Setenv("JETSCALE_CONFIG_DIR", t.TempDir())
	t.Setenv("JETSCALE_API_URL", "")
	t.Setenv("JETSCALE_INSTANCE", "")

	root := newRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"config", "show"})

	if err := root.Execute(); err != nil {
		t.Fatalf("config show failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "production") {
		t.Errorf("expected production in output, got: %q", out)
	}
}

func TestConfigInstances(t *testing.T) {
	t.Setenv("JETSCALE_CONFIG_DIR", t.TempDir())

	root := newRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"config", "instances"})

	if err := root.Execute(); err != nil {
		t.Fatalf("config instances failed: %v", err)
	}

	out := buf.String()
	for _, name := range []string{"production", "staging", "local"} {
		if !strings.Contains(out, name) {
			t.Errorf("expected %q in output, got: %q", name, out)
		}
	}
}

func TestConfigGetDefaultInstance(t *testing.T) {
	t.Setenv("JETSCALE_CONFIG_DIR", t.TempDir())

	root := newRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"config", "get", "default-instance"})

	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	out := strings.TrimSpace(buf.String())
	if out != "production" {
		t.Errorf("got %q, want %q", out, "production")
	}
}

func TestConfigSetAndGet(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("JETSCALE_CONFIG_DIR", tmp)

	// Add a custom instance
	root := newRootCmd()
	root.SetArgs([]string{"config", "set", "instance.acme", "https://jetscale.acme.com"})
	if err := root.Execute(); err != nil {
		t.Fatalf("config set instance: %v", err)
	}

	// Switch default to it
	root = newRootCmd()
	root.SetArgs([]string{"config", "set", "default-instance", "acme"})
	if err := root.Execute(); err != nil {
		t.Fatalf("config set default-instance: %v", err)
	}

	// Read it back
	root = newRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"config", "get", "default-instance"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	out := strings.TrimSpace(buf.String())
	if out != "acme" {
		t.Errorf("got %q, want %q", out, "acme")
	}
}

func TestAccountsHelp(t *testing.T) {
	root := newRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"accounts", "--help"})

	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	for _, sub := range []string{"list", "use", "current"} {
		if !strings.Contains(out, sub) {
			t.Errorf("expected %q in accounts help, got: %q", sub, out)
		}
	}
}

func TestAccountsCurrentNoAccount(t *testing.T) {
	t.Setenv("JETSCALE_CONFIG_DIR", t.TempDir())
	t.Setenv("JETSCALE_API_URL", "http://localhost:1")
	t.Setenv("JETSCALE_INSTANCE", "")
	t.Setenv("JETSCALE_ACCOUNT", "")
	t.Setenv("JETSCALE_TOKEN", "")

	root := newRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"accounts", "current"})

	// Should not error, just print guidance
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if !strings.Contains(out, "No account selected") && !strings.Contains(out, "not logged in") {
		t.Errorf("expected guidance message, got: %q", out)
	}
}
