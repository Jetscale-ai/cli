package api

import (
	"context"
	"testing"
)

func TestFlagsApprovedProfileParity(t *testing.T) {
	f := newFlagsFixture()
	p := flagsTestProfile()
	keys := []string{"AGENTS.EXPLORER", "CLOUD.AZURE.FOCUS", "CLOUD.FOCUS", "INTEGRATIONS.AABHRA"}
	for _, key := range keys {
		p.Flags.Root[key] = true
		p.Flags.Company[key] = true
	}
	client := f.server(t)
	r, err := client.ApplyFlags(context.Background(), p)
	if err != nil || !r.Satisfied || r.OutcomeUncertain {
		t.Fatalf("approved profile %+v %v", r, err)
	}
	writes := f.writeLog()
	if len(writes) != 8 {
		t.Fatalf("wrong write count %+v", writes)
	}
	for i, key := range keys {
		if writes[i].Scope != "root" || writes[i].Key != key || writes[i+4].Scope != "company" || writes[i+4].Key != key {
			t.Fatalf("wrong order %+v", writes)
		}
	}
	r, err = client.ApplyFlags(context.Background(), p)
	if err != nil || !r.Satisfied || len(f.writeLog()) != 8 {
		t.Fatal("approved profile not idempotent")
	}
}

func TestFlagsExistingCompanyBootstrapDirectBU(t *testing.T) {
	f := newFlagsFixture()
	f.flag("root", "", flagsTestKey, false)
	f.flag("company", flagsTestCompany, flagsTestKey, true)
	f.flag("business_unit", flagsTestBU, flagsTestKey, false)
	p := flagsTestProfile()
	p.CompanyCascade = ""
	r, err := f.server(t).ApplyFlags(context.Background(), p)
	writes := f.writeLog()
	if err != nil || !r.Satisfied || len(writes) != 2 || writes[0].Scope != "root" || writes[1].Scope != "business_unit" || writes[1].Method != "PATCH" {
		t.Fatalf("direct BU %+v %v %+v", r, err, writes)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rows[flagsFixtureRowKey("cloud_account", flagsTestAccount, flagsTestKey)] != nil {
		t.Fatal("BU enable cascaded")
	}
}
