//go:build mage

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeFixture(t *testing.T, path, contents string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
}

func TestInstallLocalAtomicReplacement(t *testing.T) {
	base := t.TempDir()
	source, dir := filepath.Join(base, "build", localBinary), filepath.Join(base, "user-bin")
	writeFixture(t, source, "new local binary", 0o755)
	live := filepath.Join(dir, "jetscale")
	writeFixture(t, live, "live binary", 0o755)
	target := filepath.Join(dir, localBinary)
	if err := os.Link(live, target); err != nil {
		t.Fatal(err)
	}
	old, err := os.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = old.Close() }()
	if err := installLocalFile(source, dir); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{live, source, target} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		want := "new local binary"
		if path == live {
			want = "live binary"
		}
		if string(data) != want {
			t.Fatalf("%s changed incorrectly: %q", path, data)
		}
	}
	oldData, err := io.ReadAll(old)
	if err != nil || string(oldData) != "live binary" {
		t.Fatal("replacement modified the old inode")
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("installed mode: %v %v", info, err)
	}
	if err := installLocalFile(source, dir); err != nil {
		t.Fatal("repeat install:", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("temporary file leaked: %v %v", entries, err)
	}
}

func TestInstallLocalRejectsUnsafeFiles(t *testing.T) {
	for _, kind := range []string{"target-symlink", "dangling-symlink", "target-directory", "parent-symlink", "source-symlink"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			source, dir := filepath.Join(base, "source"), filepath.Join(base, "user-bin")
			writeFixture(t, source, "local", 0o755)
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			live := filepath.Join(base, "jetscale")
			writeFixture(t, live, "live", 0o755)
			target := filepath.Join(dir, localBinary)
			var err error
			switch kind {
			case "target-symlink":
				err = os.Symlink(live, target)
			case "dangling-symlink":
				err = os.Symlink(filepath.Join(base, "absent"), target)
			case "target-directory":
				err = os.Mkdir(target, 0o755)
			case "parent-symlink":
				alias := filepath.Join(base, "alias")
				err = os.Symlink(dir, alias)
				dir = filepath.Join(alias, "nested")
			case "source-symlink":
				source = filepath.Join(base, "source-link")
				err = os.Symlink(live, source)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := installLocalFile(source, dir); err == nil {
				t.Fatal("unsafe install accepted")
			}
			data, err := os.ReadFile(live)
			if err != nil || string(data) != "live" {
				t.Fatal("live binary changed")
			}
		})
	}
}

func TestLocalLDFlagsProvenance(t *testing.T) {
	t.Setenv("JETSCALE_LOCAL_BACKEND_SHA", "")
	t.Setenv("VERSION", "must-not-leak")
	t.Setenv("COMMIT", "must-not-leak")
	flags, err := localLDFlags()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("openapi/spec.json")
	if err != nil {
		t.Fatal(err)
	}
	schema := sha256.Sum256(raw)
	for _, expected := range []string{
		"/internal/buildinfo.Channel=local", "/internal/cmd.version=local",
		"/internal/cmd.commit=", "/internal/cmd.date=",
		"/internal/buildinfo.BackendSHA=unknown", "/internal/buildinfo.SchemaSHA256=",
		"/internal/buildinfo.SourceSHA256=", "/internal/buildinfo.SourceDirty=",
	} {
		if !strings.Contains(flags, expected) {
			t.Errorf("missing %q", expected)
		}
	}
	if !strings.Contains(flags, hex.EncodeToString(schema[:])) || strings.Contains(flags, "must-not-leak") {
		t.Fatal("schema hash or isolated metadata incorrect")
	}
	t.Setenv("JETSCALE_LOCAL_BACKEND_SHA", "bad' -X injection")
	if _, err := localLDFlags(); err == nil {
		t.Fatal("unsafe linker metadata accepted")
	}
}

func TestGenerateUsesExactPin(t *testing.T) {
	const generation = "run\n" + oapiCodegenModule + "@v2.6.0\n-package\ngenerated\n-generate\ntypes,client\n-o\ninternal/api/generated/client.gen.go\nopenapi/spec.json\n"
	for _, target := range []struct {
		name string
		run  func() error
		want string
	}{
		{"GenerateClientNoTidy", GenerateClient, generation},
		{"GenerateWithTidy", Generate, generation + "mod\ntidy\n"},
	} {
		t.Run(target.name, func(t *testing.T) {
			base := t.TempDir()
			t.Chdir(base)
			writeFixture(t, "openapi/spec.json", "{}", 0o644)
			calls := filepath.Join(base, "calls")
			t.Setenv("TEST_GO_CALLS", calls)
			toolDir := filepath.Join(base, "tools")
			const record = "#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$TEST_GO_CALLS\"\n"
			writeFixture(t, filepath.Join(toolDir, "go"), record+"exit 0\n", 0o755)
			writeFixture(t, filepath.Join(toolDir, "oapi-codegen"), "#!/bin/sh\nexit 99\n", 0o755)
			t.Setenv("PATH", toolDir)
			if err := target.run(); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(calls)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != target.want {
				t.Fatalf("go invocations = %q, want %q", data, target.want)
			}

			writeFixture(t, calls, "", 0o644)
			writeFixture(t, filepath.Join(toolDir, "go"), record+"exit 17\n", 0o755)
			if err := target.run(); err == nil {
				t.Fatal("generator failure swallowed")
			}
			data, err = os.ReadFile(calls)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != generation {
				t.Fatalf("generation failure must stop before tidy: %q", data)
			}
		})
	}
}

func TestSpecTLSVerification(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "{}") }))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	defer srv.Close()
	untrusted, err := specHTTPClient("")
	if err != nil {
		t.Fatal(err)
	}
	defer untrusted.CloseIdleConnections()
	if resp, err := untrusted.Get(srv.URL); err == nil {
		_ = resp.Body.Close()
		t.Fatal("untrusted certificate accepted")
	}
	ca := filepath.Join(t.TempDir(), "ca.pem")
	writeFixture(t, ca, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})), 0o644)
	trusted, err := specHTTPClient(ca)
	if err != nil {
		t.Fatal(err)
	}
	defer trusted.CloseIdleConnections()
	resp, err := trusted.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	trusted.CloseIdleConnections()
	trusted.Transport.(*http.Transport).TLSClientConfig.ServerName = "jetscale.internal"
	if resp, err := trusted.Get(srv.URL); err == nil {
		_ = resp.Body.Close()
		t.Fatal("hostname mismatch accepted")
	}
	writeFixture(t, ca, "not a certificate", 0o644)
	if _, err := specHTTPClient(ca); err == nil {
		t.Fatal("invalid CA accepted")
	}
	if _, err := specHTTPClient(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing CA accepted")
	}
}

// These are the six optional string discriminators in the current Backend export.
var discriminatorVariants = []struct {
	name, property, value string
	required              []string
}{
	{"AwsS3CostDataSourceWriteConfig", "source_kind", "focus_s3", []string{"bucket", "prefix"}},
	{"GcpBigQueryCostDataSourceWriteConfig", "source_kind", "focus_bigquery", []string{"export_project_id", "dataset", "table", "credential_cloud_account_id"}},
	{"AzureBlobCostDataSourceWriteConfig", "source_kind", "focus_azure_blob", []string{"storage_account", "container", "prefix", "credential_cloud_account_id"}},
	{"CloudAccountScope", "kind", "cloud_account", []string{"cloud_account_id"}},
	{"BusinessUnitScope", "kind", "business_unit", []string{"business_unit_id"}},
	{"CompanyScope", "kind", "company", []string{"company_id"}},
}

func discriminatorFixture() map[string]interface{} {
	schemas := map[string]interface{}{}
	for _, variant := range discriminatorVariants {
		properties := map[string]interface{}{
			variant.property: map[string]interface{}{"type": "string", "const": variant.value, "default": variant.value},
			"unrelated":      map[string]interface{}{"type": "string", "default": "unchanged"},
		}
		required := []interface{}{}
		for _, name := range variant.required {
			required = append(required, name)
			properties[name] = map[string]interface{}{"type": "string"}
		}
		schemas[variant.name] = map[string]interface{}{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	}
	for i, name := range []string{"CloudCostDataSourceUpsertRequest", "CreateExplorerSessionRequest"} {
		oneOf := []interface{}{}
		mapping := map[string]interface{}{}
		for _, variant := range discriminatorVariants[i*3 : i*3+3] {
			ref := "#/components/schemas/" + variant.name
			oneOf = append(oneOf, map[string]interface{}{"$ref": ref})
			mapping[variant.value] = ref
		}
		property := []string{"provider_config", "scope"}[i]
		schemas[name] = map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			property: map[string]interface{}{"oneOf": oneOf, "discriminator": map[string]interface{}{"propertyName": discriminatorVariants[i*3].property, "mapping": mapping}},
		}}
	}
	return map[string]interface{}{"openapi": "3.1.0", "components": map[string]interface{}{"schemas": schemas}, "paths": map[string]interface{}{
		"/api/v2/fixture": map[string]interface{}{"post": map[string]interface{}{"requestBody": map[string]interface{}{"content": map[string]interface{}{"application/json": map[string]interface{}{"schema": map[string]interface{}{"anyOf": []interface{}{
			map[string]interface{}{"$ref": "#/components/schemas/CloudCostDataSourceUpsertRequest"},
			map[string]interface{}{"$ref": "#/components/schemas/CreateExplorerSessionRequest"},
		}}}}}}},
	}}
}

func TestOptionalDiscriminatorPointers(t *testing.T) {
	for _, keyword := range []string{"oneOf", "anyOf"} {
		t.Run(keyword, func(t *testing.T) {
			spec := discriminatorFixture()
			if keyword == "anyOf" {
				schemas := spec["components"].(map[string]interface{})["schemas"].(map[string]interface{})
				for _, pair := range [][2]string{{"CloudCostDataSourceUpsertRequest", "provider_config"}, {"CreateExplorerSessionRequest", "scope"}} {
					union := schemas[pair[0]].(map[string]interface{})["properties"].(map[string]interface{})[pair[1]].(map[string]interface{})
					union["anyOf"] = union["oneOf"]
					delete(union, "oneOf")
				}
			}
			before, _ := json.Marshal(spec)
			patchOptionalDiscriminatorPointers(spec)
			after, _ := json.Marshal(spec)
			patchOptionalDiscriminatorPointers(spec)
			repeated, _ := json.Marshal(spec)
			if string(after) != string(repeated) {
				t.Fatal("transform is not idempotent")
			}
			schemas := spec["components"].(map[string]interface{})["schemas"].(map[string]interface{})
			for _, variant := range discriminatorVariants {
				property := schemas[variant.name].(map[string]interface{})["properties"].(map[string]interface{})[variant.property].(map[string]interface{})
				if property["x-go-type-skip-optional-pointer"] != true {
					t.Errorf("%s.%s missing codegen extension", variant.name, variant.property)
				}
				delete(property, "x-go-type-skip-optional-pointer")
			}
			restored, _ := json.Marshal(spec)
			if string(before) != string(restored) {
				t.Fatal("transform changed more than the six pointer extensions (including required/default/const)")
			}
		})
	}
}

func TestOptionalDiscriminatorPointersIgnoresOtherSchemas(t *testing.T) {
	for _, kind := range []string{"required", "nullable", "non-string", "unconstrained", "empty-const", "property-ref", "missing-property", "missing-discriminator", "external-ref", "missing-ref", "nested-ref", "allOf", "example", "default"} {
		t.Run(kind, func(t *testing.T) {
			property := map[string]interface{}{"type": "string", "const": "variant", "default": "variant"}
			target := map[string]interface{}{"properties": map[string]interface{}{"kind": property}}
			variant := map[string]interface{}{"$ref": "#/components/schemas/Variant"}
			union := map[string]interface{}{"oneOf": []interface{}{variant}, "discriminator": map[string]interface{}{"propertyName": "kind"}}
			schemas := map[string]interface{}{"Variant": target, "Union": union}
			switch kind {
			case "required":
				target["required"] = []interface{}{"kind"}
			case "nullable":
				property["nullable"] = true
			case "non-string":
				property["type"] = "integer"
			case "unconstrained":
				delete(property, "const")
			case "empty-const":
				property["const"] = ""
			case "property-ref":
				property["$ref"] = "#/components/schemas/Other"
			case "missing-property":
				delete(target, "properties")
			case "missing-discriminator":
				delete(union, "discriminator")
			case "external-ref":
				variant["$ref"] = "https://example.invalid/spec.json#/components/schemas/Variant"
			case "missing-ref":
				variant["$ref"] = "#/components/schemas/Missing"
			case "nested-ref":
				variant["$ref"] = "#/components/schemas/Variant/properties/kind"
			case "allOf":
				union["allOf"] = union["oneOf"]
				delete(union, "oneOf")
			case "example", "default":
				schemas["Union"] = map[string]interface{}{kind: union}
			}
			spec := map[string]interface{}{"components": map[string]interface{}{"schemas": schemas}}
			before, _ := json.Marshal(spec)
			patchOptionalDiscriminatorPointers(spec)
			after, _ := json.Marshal(spec)
			if string(before) != string(after) {
				t.Fatalf("unexpected mutation: %s", after)
			}
		})
	}
}

func TestProcessSpecOptionalDiscriminators(t *testing.T) {
	t.Chdir(t.TempDir())
	spec := discriminatorFixture()
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, "openapi/spec.json", string(raw), 0o644)
	if err := ProcessSpec(); err != nil {
		t.Fatal(err)
	}
	processed, err := os.ReadFile("openapi/spec.json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(processed, &got); err != nil {
		t.Fatal(err)
	}
	spec["openapi"] = "3.0.3"
	patchOptionalDiscriminatorPointers(spec)
	if !reflect.DeepEqual(got, spec) {
		t.Fatal("ProcessSpec did not preserve the fixture with only version and pointer compatibility changes")
	}
}

func TestDeduplicateOperationIDsDeterministicCollisions(t *testing.T) {
	operations := []struct {
		path, method, original, want string
	}{
		{"/a", "get", "fetch", "fetch"},
		{"/a", "post", "fetch", "fetch_post_2"},
		{"/b", "get", "fetch", "fetch_get_3"},
		{"/c", "get", "fetch", "fetch_get_4"},
		{"/d", "get", "fetch_get", "fetch_get"},
		{"/e", "get", "fetch_get", "fetch_get_get_2"},
		{"/z", "get", "fetch_get_2", "fetch_get_2"},
		{"/z", "post", "fetch_post", "fetch_post"},
		{"/z", "put", "fetch_get_get", "fetch_get_get"},
		{"/zz", "delete", "fetch", "fetch_delete"},
	}
	for _, reverse := range []bool{false, true} {
		paths := map[string]interface{}{}
		for i := range operations {
			if reverse {
				i = len(operations) - 1 - i
			}
			op := operations[i]
			methods, ok := paths[op.path].(map[string]interface{})
			if !ok {
				methods = map[string]interface{}{}
				paths[op.path] = methods
			}
			methods[op.method] = map[string]interface{}{"operationId": op.original, "summary": "unchanged"}
		}
		spec := map[string]interface{}{"paths": paths}
		deduplicateOperationIDs(spec)
		for pass := 0; pass < 2; pass++ {
			seen := map[string]bool{}
			for _, op := range operations {
				got := paths[op.path].(map[string]interface{})[op.method].(map[string]interface{})
				if !reflect.DeepEqual(got, map[string]interface{}{"operationId": op.want, "summary": "unchanged"}) {
					t.Fatalf("reverse=%v pass=%d %s %s: got %v, want ID %s", reverse, pass, op.method, op.path, got, op.want)
				}
				id := got["operationId"].(string)
				if seen[id] {
					t.Fatalf("duplicate resulting ID %s", id)
				}
				seen[id] = true
			}
			// A second pass must preserve every previously assigned ID.
			if pass == 0 {
				deduplicateOperationIDs(spec)
			}
		}
	}
}

func TestDeduplicateOperationIDsIgnoresMissingIDs(t *testing.T) {
	for _, fixture := range []string{
		`{}`,
		`{"paths":null}`,
		`{"paths":{"/empty":null,"/items":{"parameters":[],"summary":"items","get":{},"post":{"operationId":""},"put":{"operationId":42}}}}`,
	} {
		var spec map[string]interface{}
		if err := json.Unmarshal([]byte(fixture), &spec); err != nil {
			t.Fatal(err)
		}
		before, _ := json.Marshal(spec)
		deduplicateOperationIDs(spec)
		after, _ := json.Marshal(spec)
		if string(before) != string(after) {
			t.Fatalf("changed non-operation data: %s", after)
		}
	}
}

func TestDeduplicateOperationIDsCheckedSchemaUnchanged(t *testing.T) {
	raw, err := os.ReadFile("openapi/spec.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]interface{}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	deduplicateOperationIDs(spec)
	processed, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if string(append(processed, '\n')) != string(raw) {
		t.Fatal("deduplication would change the checked-in schema; review before regeneration")
	}
	t.Logf("checked-in schema unchanged: SHA256 %x", sha256.Sum256(raw))
}

func TestSpecRedirectRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/elsewhere", http.StatusFound) }))
	defer srv.Close()
	client, err := specHTTPClient("")
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	if resp, err := client.Get(srv.URL); err == nil {
		_ = resp.Body.Close()
		t.Fatal("redirect must not silently change spec source")
	}
}
