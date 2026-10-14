package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func flagsBudgetFixture(accounts int) (*flagsFixture, FlagsProfile) {
	f := newFlagsFixture()
	f.units = f.units[:1]
	f.accounts = []map[string]any{}
	p := flagsTestProfile()
	for i := 0; i < accounts; i++ {
		id := fmt.Sprintf("52000000-0000-0000-0000-%012d", i+1)
		f.accounts = append(f.accounts, map[string]any{"id": id, "business_unit": flagsTestBU})
	}
	return f, p
}

func TestFlagsRequestBudgetDefaultProfileAndRerun(t *testing.T) {
	if flagsMaxRequests != 1000 {
		t.Fatalf("request limit changed: %d", flagsMaxRequests)
	}
	f, p := flagsBudgetFixture(3)
	for _, key := range []string{flagsTestKey, "CLOUD.AZURE.FOCUS", "CLOUD.FOCUS", "INTEGRATIONS.AABHRA"} {
		p.Flags.Root[key], p.Flags.Company[key] = true, true
	}
	client := f.server(t)
	plan, err := client.PlanFlags(context.Background(), p)
	if err != nil || len(plan.Blockers) != 0 || len(plan.Actions) != 24 {
		t.Fatalf("default plan rejected: %+v %v", plan, err)
	}
	f.mu.Lock()
	initialReads := len(f.reads)
	f.mu.Unlock()
	result, err := client.ApplyFlags(context.Background(), p)
	if err != nil || !result.Satisfied || result.OutcomeUncertain || len(result.Blockers) != 0 || len(f.writeLog()) != 8 {
		t.Fatalf("default apply rejected: %+v %v", result, err)
	}
	f.mu.Lock()
	requests := len(f.reads) - initialReads + len(f.writes)
	f.mu.Unlock()
	if requests != 484 || requests > 1000 {
		t.Fatalf("unexpected default request count: %d", requests)
	}
	result, err = client.ApplyFlags(context.Background(), p)
	if err != nil || !result.Satisfied || len(result.Blockers) != 0 || len(f.writeLog()) != 8 {
		t.Fatalf("default rerun mutated or blocked: %+v %v", result, err)
	}
	for _, action := range result.Actions {
		if action.Status != "noop" {
			t.Fatalf("satisfied action not noop: %+v", action)
		}
	}
}

func TestFlagsRequestBudgetDirectWriteBoundary(t *testing.T) {
	for _, accounts := range []int{17, 18, 99} {
		t.Run(fmt.Sprint(accounts), func(t *testing.T) {
			f, p := flagsBudgetFixture(accounts)
			f.flag("root", "", flagsTestKey, true)
			f.flag("company", flagsTestCompany, flagsTestKey, true)
			f.flag("business_unit", flagsTestBU, flagsTestKey, true)
			p.CompanyCascade = ""
			for i, account := range f.accounts {
				id := account["id"].(string)
				p.Flags.CloudAccounts = append(p.Flags.CloudAccounts, FlagsAccountLayer{ID: uuid.MustParse(id), Flags: map[string]bool{flagsTestKey: true}})
				if accounts == 99 && i > 0 {
					f.flag("cloud_account", id, flagsTestKey, true)
				}
			}
			result, err := f.server(t).ApplyFlags(context.Background(), p)
			if err != nil || result.OutcomeUncertain {
				t.Fatalf("unexpected failure: %+v %v", result, err)
			}
			if accounts == 18 {
				if result.Satisfied || len(result.Blockers) != 1 || result.Blockers[0].Reason != "request_budget_exceeded" || len(f.writeLog()) != 0 {
					t.Fatalf("over-boundary apply not blocked: %+v", result)
				}
				return
			}
			writes := 17
			if accounts == 99 {
				writes = 1 // two-page topology with 98 noop account actions
			}
			if !result.Satisfied || len(result.Blockers) != 0 || len(f.writeLog()) != writes {
				t.Fatalf("below-budget apply blocked: %+v", result)
			}
			f.mu.Lock()
			requests := len(f.reads) + len(f.writes)
			f.mu.Unlock()
			if requests > 1000 {
				t.Fatalf("budget exceeded: %d", requests)
			}
		})
	}
}

func TestFlagsRequestBudgetNoopsAndShowRemainUsable(t *testing.T) {
	for _, mode := range []string{"show", "apply"} {
		t.Run(mode, func(t *testing.T) {
			f, p := flagsBudgetFixture(99)
			for _, key := range []string{flagsTestKey, "CLOUD.AZURE.FOCUS", "CLOUD.FOCUS", "INTEGRATIONS.AABHRA"} {
				p.Flags.Root[key], p.Flags.Company[key] = true, true
				f.flag("root", "", key, true)
				f.flag("company", flagsTestCompany, key, true)
				f.flag("business_unit", flagsTestBU, key, true)
			}
			for _, account := range f.accounts {
				id := account["id"].(string)
				values := map[string]bool{}
				for key := range p.Flags.Root {
					values[key] = true
					if mode == "apply" {
						f.flag("cloud_account", id, key, true)
					}
				}
				p.Flags.CloudAccounts = append(p.Flags.CloudAccounts, FlagsAccountLayer{ID: uuid.MustParse(id), Flags: values})
			}
			client := f.server(t)
			var result FlagsResult
			var err error
			if mode == "show" {
				result, err = client.ShowFlags(context.Background(), p)
			} else {
				result, err = client.ApplyFlags(context.Background(), p)
			}
			if err != nil || len(result.Blockers) != 0 || result.OutcomeUncertain || len(f.writeLog()) != 0 || (mode == "apply" && !result.Satisfied) {
				t.Fatalf("read-only/noop profile blocked: err=%v blockers=%+v", err, result.Blockers)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.reads) != 413 {
				t.Fatalf("unexpected reinspection: %d", len(f.reads))
			}
		})
	}
}

func TestFlagsRequestBudgetRefusesBeforeAnyMutation(t *testing.T) {
	for _, mode := range []string{"plan", "apply"} {
		t.Run(mode, func(t *testing.T) {
			f, p := flagsBudgetFixture(99)
			keys := []string{flagsTestKey, "CLOUD.AZURE.FOCUS", "CLOUD.FOCUS", "INTEGRATIONS.AABHRA"}
			for _, key := range keys {
				p.Flags.Root[key], p.Flags.Company[key] = true, true
				f.flag("root", "", key, true)
				f.flag("company", flagsTestCompany, key, true)
				f.flag("business_unit", flagsTestBU, key, true)
			}
			for _, account := range f.accounts {
				values := map[string]bool{}
				for _, key := range keys {
					values[key] = true
				}
				p.Flags.CloudAccounts = append(p.Flags.CloudAccounts, FlagsAccountLayer{ID: uuid.MustParse(account["id"].(string)), Flags: values})
			}
			attempts := 0
			// Keep the red run bounded: reject the first write instead of allowing
			// the old implementation's hundreds of thousands of requests.
			f.hook = func(w http.ResponseWriter, r *http.Request) bool {
				if r.Method != http.MethodGet {
					attempts++
					w.WriteHeader(http.StatusServiceUnavailable)
					return true
				}
				return false
			}
			client := f.server(t)
			var result FlagsResult
			var err error
			if mode == "plan" {
				result, err = client.PlanFlags(context.Background(), p)
			} else {
				result, err = client.ApplyFlags(context.Background(), p)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if err != nil || result.Satisfied || result.OutcomeUncertain || attempts != 0 || len(result.Blockers) != 1 || result.Blockers[0].Reason != "request_budget_exceeded" {
				t.Fatalf("budget not refused before mutation: err=%v attempts=%d blockers=%+v uncertain=%v", err, attempts, result.Blockers, result.OutcomeUncertain)
			}
			if len(f.reads) != 413 || len(result.States) != 408 {
				t.Fatalf("expected initial inspection only: reads=%d states=%d", len(f.reads), len(result.States))
			}
			for _, action := range result.Actions {
				if action.Scope == "cloud_account" {
					if action.Status != "blocked" || action.Reason != "request_budget_exceeded" {
						t.Fatalf("unbounded account action: %+v", action)
					}
				} else if action.Status != "noop" {
					t.Fatalf("satisfied prerequisite altered: %+v", action)
				}
			}
		})
	}
}
