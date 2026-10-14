package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

func TestFlagsRejectRedirectedWrites(t *testing.T) {
	var followed atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { followed.Add(1); flagsFixtureJSON(w, map[string]any{}) }))
	defer destination.Close()
	f := newFlagsFixture()
	f.hook = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method != "GET" {
			http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
			return true
		}
		return false
	}
	r, err := f.server(t).ApplyFlags(context.Background(), flagsTestProfile())
	if err == nil || !r.OutcomeUncertain || followed.Load() != 0 {
		t.Fatalf("redirect followed %+v %v %d", r, err, followed.Load())
	}
}

func TestFlagsDirectAccountWithoutBUSelector(t *testing.T) {
	f := newFlagsFixture()
	for _, ref := range [][2]string{{"root", ""}, {"company", flagsTestCompany}, {"business_unit", flagsTestBU2}} {
		f.flag(ref[0], ref[1], flagsTestKey, true)
	}
	p := FlagsProfile{Version: 1, Company: flagsTestProfile().Company, Flags: FlagsLayers{CloudAccounts: []FlagsAccountLayer{{ID: uuid.MustParse(flagsTestAccount2), Flags: map[string]bool{flagsTestKey: true}}}}}
	r, err := f.server(t).ApplyFlags(context.Background(), p)
	writes := f.writeLog()
	if err != nil || !r.Satisfied || r.BusinessUnitID != "" || len(writes) != 1 || writes[0].ID != flagsTestAccount2 {
		t.Fatalf("direct account %+v %v %+v", r, err, writes)
	}
}

func TestFlagsStoredMissingFalseTrue(t *testing.T) {
	for _, value := range []string{"missing", "false", "true"} {
		t.Run(value, func(t *testing.T) {
			f := newFlagsFixture()
			if value != "missing" {
				f.flag("root", "", flagsTestKey, value == "true")
			}
			p := FlagsProfile{Version: 1, Company: flagsTestProfile().Company, Flags: FlagsLayers{Root: map[string]bool{flagsTestKey: true}}}
			r, err := f.server(t).ShowFlags(context.Background(), p)
			if err != nil || len(r.States) != 1 {
				t.Fatalf("show %+v %v", r, err)
			}
			s := r.States[0]
			if (s.Stored == nil) != (value == "missing") || s.Effective == nil || *s.Effective != (value == "true") {
				t.Fatalf("wrong state %+v", s)
			}
			if s.Stored != nil && fmt.Sprint(*s.Stored) != value {
				t.Fatal("stored mismatch")
			}
		})
	}
}
