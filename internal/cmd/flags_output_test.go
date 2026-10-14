package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Jetscale-ai/cli/internal/api"
	"github.com/Jetscale-ai/cli/internal/output"
	"gopkg.in/yaml.v3"
)

func TestFlagsResultStreamsAndFields(t *testing.T) {
	yes, no := true, false
	result := api.FlagsResult{
		Mode: "plan", CompanyID: "company-id", BusinessUnitID: "unit-id", RootApplicationWide: true,
		States: []api.FlagsState{
			{Scope: "root", Key: "AGENTS.EXPLORER", Stored: &no, Effective: &no, Reason: "stored_false"},
			{Scope: "company", EntityID: "company-id", Key: "CLOUD.FOCUS", Stored: nil, Effective: &no, Reason: "stored_missing"},
			{Scope: "business_unit", EntityID: "unit-id", Key: "INTEGRATIONS.AABHRA", Reason: "unsafe_flag"},
			{Scope: "root", Key: "CLOUD.AZURE.FOCUS", Stored: &yes, Effective: &yes, Reason: "enabled"},
		},
		Actions: []api.FlagsAction{
			{Scope: "root", Key: "AGENTS.EXPLORER", Operation: "set", Status: "planned", Reason: "enable"},
			{Scope: "company", EntityID: "company-id", Key: "CLOUD.FOCUS", Operation: "create", Status: "planned", Cascade: true, Reason: "enable"},
			{Scope: "business_unit", EntityID: "unit-id", Key: "CLOUD.FOCUS", Operation: "cascade_enable", Status: "planned", Cascade: true, Reason: "company_cascade"},
			{Scope: "root", Key: "CLOUD.AZURE.FOCUS", Operation: "set", Status: "noop", Reason: "already_enabled"},
		},
		Blockers: []api.FlagsBlocker{},
	}
	for _, format := range []output.Format{output.JSON, output.YAML, output.Table} {
		t.Run(string(format), func(t *testing.T) {
			t.Setenv("NO_COLOR", "1")
			t.Setenv("TERM", "dumb")
			var stdout bytes.Buffer
			if err := printFlagsResult(output.Printer{Format: format, Out: &stdout}, "plan", result, nil); err != nil {
				t.Fatalf("planned changes should not fail observation: %v", err)
			}
			out := stdout.String()
			if strings.ContainsAny(out, "\x1b\r") {
				t.Fatal("monochrome result contains ANSI/cursor control")
			}
			if format == output.Table {
				if strings.Contains(out, "missing/unknown") {
					t.Error("stored_missing and unobservable stored values must have distinct labels")
				}
				for _, text := range []string{"STORED", "EFFECTIVE", "missing", "unknown", "false", "true", "planned", "noop", "CASCADE (ALL DESCENDANTS)", "cascade_enable", "Root application-wide: true"} {
					if !strings.Contains(out, text) {
						t.Errorf("table missing %q: %s", text, out)
					}
				}
				return
			}
			var decoded map[string]any
			var err error
			if format == output.JSON {
				err = json.Unmarshal(stdout.Bytes(), &decoded)
			} else {
				err = yaml.Unmarshal(stdout.Bytes(), &decoded)
			}
			if err != nil {
				t.Fatalf("unparsable machine result: %v", err)
			}
			if decoded["mode"] != "plan" || decoded["company_id"] != "company-id" || decoded["business_unit_id"] != "unit-id" || decoded["root_application_wide"] != true || decoded["outcome_uncertain"] != false || decoded["satisfied"] != false {
				t.Fatalf("stable result names/values lost: %+v", decoded)
			}
			states := decoded["states"].([]any)
			missing := states[1].(map[string]any)
			if missing["stored"] != nil || missing["effective"] != false || missing["entity_id"] != "company-id" {
				t.Fatalf("nullable booleans/field names lost: %+v", missing)
			}
			actions := decoded["actions"].([]any)
			if actions[1].(map[string]any)["cascade"] != true || actions[1].(map[string]any)["status"] != "planned" {
				t.Fatal("cascade/status lost")
			}
		})
	}
}

func TestFlagsResultExitSemanticsAndRedaction(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		result     api.FlagsResult
		requestErr error
		fail       bool
	}{
		{"show-missing", "show", api.FlagsResult{States: []api.FlagsState{{Reason: "stored_missing"}}}, nil, false},
		{"plan-unsatisfied", "plan", api.FlagsResult{Actions: []api.FlagsAction{{Status: "planned"}}}, nil, false},
		{"show-noop", "show", api.FlagsResult{Satisfied: true, Actions: []api.FlagsAction{{Status: "noop"}}}, nil, false},
		{"apply-noop", "apply", api.FlagsResult{Satisfied: true, Actions: []api.FlagsAction{{Status: "noop"}}}, nil, false},
		{"apply-applied", "apply", api.FlagsResult{Satisfied: true, Actions: []api.FlagsAction{{Status: "applied", Cascade: true}}}, nil, false},
		{"apply-unsatisfied", "apply", api.FlagsResult{Actions: []api.FlagsAction{{Status: "applied"}}}, nil, true},
		{"apply-incomplete", "apply", api.FlagsResult{Satisfied: true, Actions: []api.FlagsAction{{Status: "planned"}}}, nil, true},
		{"blocker", "show", api.FlagsResult{Blockers: []api.FlagsBlocker{{Reason: "company_selector_mismatch"}}}, nil, true},
		{"blocked-status", "plan", api.FlagsResult{Actions: []api.FlagsAction{{Status: "blocked"}}}, nil, true},
		{"uncertain-apply", "apply", api.FlagsResult{Satisfied: true, OutcomeUncertain: true}, nil, true},
		{"uncertain-action", "apply", api.FlagsResult{Satisfied: true, Actions: []api.FlagsAction{{Status: "outcome_uncertain"}}}, nil, true},
		{"request-failure", "show", api.FlagsResult{}, errors.New("sensitive-sentinel: raw response body"), true},
		{"apply-request-failure", "apply", api.FlagsResult{Satisfied: true}, errors.New("sensitive-sentinel: raw URL"), true},
		{"invalid-status", "apply", api.FlagsResult{Satisfied: true, Actions: []api.FlagsAction{{Status: "sensitive-sentinel"}}}, nil, true},
	} {
		for _, format := range []output.Format{output.JSON, output.YAML, output.Table} {
			t.Run(tc.name+"/"+string(format), func(t *testing.T) {
				var stdout bytes.Buffer
				err := printFlagsResult(output.Printer{Format: format, Out: &stdout}, tc.mode, tc.result, tc.requestErr)
				if (err != nil) != tc.fail {
					t.Fatalf("exit semantics: wanted failure=%t got %v", tc.fail, err)
				}
				if stdout.Len() == 0 {
					t.Fatal("failure/success discarded sanitized result")
				}
				if err != nil && strings.Contains(err.Error(), "sensitive-sentinel") {
					t.Fatal("raw error leaked")
				}
				if tc.requestErr != nil && strings.Contains(stdout.String(), "sensitive-sentinel") {
					t.Fatal("raw request error leaked into result")
				}
			})
		}
	}
}

type flagsFailedOutput struct{}

func (flagsFailedOutput) Write([]byte) (int, error) {
	return 0, errors.New("sensitive-sentinel: writer failure")
}

func TestFlagsOutputFailure(t *testing.T) {
	for _, format := range []output.Format{output.JSON, output.YAML, output.Table} {
		err := printFlagsResult(output.Printer{Format: format, Out: flagsFailedOutput{}}, "show", api.FlagsResult{Satisfied: true}, nil)
		if err == nil || strings.Contains(err.Error(), "sensitive-sentinel") {
			t.Fatalf("output failure ignored/leaked: %v", err)
		}
	}
}

func TestFlagsValidateOnlyOutputFailure(t *testing.T) {
	for _, format := range []string{"json", "yaml", "table"} {
		t.Run(format, func(t *testing.T) {
			flagsCommandTest(t, "unstable")
			root := newRootCmd()
			root.SetOut(flagsFailedOutput{})
			root.SetErr(io.Discard)
			root.SetArgs([]string{"flags", "show", "--profile", flagsProfileFile(t, flagsProfileFixture), "--validate-only", "--output", format})
			if err := root.Execute(); err == nil || strings.Contains(err.Error(), "sensitive-sentinel") {
				t.Fatalf("offline output failure ignored/leaked: %v", err)
			}
		})
	}
}

func TestFlagsTableEscapesControlCharacters(t *testing.T) {
	var stdout bytes.Buffer
	result := api.FlagsResult{Mode: "show", CompanyID: "company\x1b[31m", States: []api.FlagsState{{Reason: "unknown\nforged\trow\r"}}}
	if err := printFlagsResult(output.Printer{Format: output.Table, Out: &stdout}, "show", result, nil); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(stdout.String(), "\x1b\r") || strings.Contains(stdout.String(), "\nforged") {
		t.Fatalf("table control characters not escaped: %q", stdout.String())
	}
}
