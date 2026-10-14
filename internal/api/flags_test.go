package api

import "testing"

func TestValidateFlagsProfileEnableOnly(t *testing.T) {
	p := FlagsProfile{Version: 1, Company: FlagsEntitySelector{Name: "Test Company", Slug: "test-company"}, Flags: FlagsLayers{Root: map[string]bool{"AGENTS.EXPLORER": true}}}
	if err := ValidateFlagsProfile(p); err != nil {
		t.Fatalf("approved enable-only profile rejected: %v", err)
	}
	p.Flags.Root["AGENTS.EXPLORER"] = false
	if err := ValidateFlagsProfile(p); err == nil {
		t.Fatal("disable must be rejected")
	}
}
