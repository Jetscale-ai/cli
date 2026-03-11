//go:build mage

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
)

var Default = Build

const (
	module = "github.com/Jetscale-ai/cli"
	bin    = "bin/jetscale"
)

// -----------------------------------------------------------------------------
// ENV (Local Dev QoL)
// -----------------------------------------------------------------------------

// loadDotEnvIfPresent loads KEY=VALUE pairs from a local .env file into the
// current process environment without overriding variables that are already set.
// Mirrors the pattern from ../stack/magefile.go for cross-repo consistency.
func loadDotEnvIfPresent(path string) (bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if fi.IsDir() {
		return false, fmt.Errorf("%s is a directory, expected a file", path)
	}

	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")

		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" {
			continue
		}

		if len(v) >= 2 {
			if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
				v = v[1 : len(v)-1]
			}
		}

		if os.Getenv(k) != "" {
			continue
		}
		_ = os.Setenv(k, v)
	}
	return sc.Err() == nil, sc.Err()
}

// -----------------------------------------------------------------------------
// BUILD
// -----------------------------------------------------------------------------

func ldflags() string {
	v := envOrDefault("VERSION", gitOutputOr("dev", "describe", "--tags", "--always", "--dirty"))
	c := envOrDefault("COMMIT", gitOutputOr("none", "rev-parse", "--short", "HEAD"))
	d := time.Now().UTC().Format(time.RFC3339)
	return strings.Join([]string{
		"-s", "-w",
		fmt.Sprintf("-X '%s/internal/cmd.version=%s'", module, v),
		fmt.Sprintf("-X '%s/internal/cmd.commit=%s'", module, c),
		fmt.Sprintf("-X '%s/internal/cmd.date=%s'", module, d),
	}, " ")
}

// Build compiles the CLI binary to bin/jetscale.
func Build() error {
	fmt.Println("Building", bin, "…")
	return sh.RunV("go", "build", "-ldflags", ldflags(), "-o", bin, "./cmd/jetscale")
}

// CrossBuild builds for all release-targeted OS/arch pairs.
func CrossBuild() error {
	type target struct{ goos, goarch string }
	targets := []target{
		{"linux", "amd64"}, {"linux", "arm64"},
		{"darwin", "amd64"}, {"darwin", "arm64"},
		{"windows", "amd64"}, {"windows", "arm64"},
	}
	for _, t := range targets {
		out := fmt.Sprintf("bin/jetscale_%s_%s", t.goos, t.goarch)
		if t.goos == "windows" {
			out += ".exe"
		}
		fmt.Printf("Building %s/%s → %s\n", t.goos, t.goarch, out)
		env := map[string]string{
			"GOOS":        t.goos,
			"GOARCH":      t.goarch,
			"CGO_ENABLED": "0",
		}
		if err := sh.RunWith(env, "go", "build", "-ldflags", ldflags(), "-o", out, "./cmd/jetscale"); err != nil {
			return err
		}
	}
	return nil
}

// Clean removes build artefacts.
func Clean() error {
	fmt.Println("Cleaning bin/ …")
	return os.RemoveAll("bin")
}

// -----------------------------------------------------------------------------
// QUALITY
// -----------------------------------------------------------------------------

// Lint runs golangci-lint.
func Lint() error {
	return sh.RunV("golangci-lint", "run")
}

// Test runs all Go tests with race detection.
func Test() error {
	return sh.RunWith(map[string]string{"CGO_ENABLED": "1"}, "go", "test", "-race", "./...")
}

// All runs lint, test, and build (the full local CI check).
func All() {
	mg.SerialDeps(Lint, Test, Build)
}

// -----------------------------------------------------------------------------
// CODEGEN
// -----------------------------------------------------------------------------

var localProbePorts = []string{
	"http://localhost:8000",
	"http://localhost:8010",
}

// SyncSpec fetches the OpenAPI spec from a running local backend, filters to
// /api/v2 paths, prunes orphaned schemas, and writes openapi/spec.json.
// The backend is auto-detected at localhost:8000 or localhost:8010.
func SyncSpec() error {
	return syncSpecFrom("")
}

// SyncSpecFrom is like SyncSpec but targets an explicit backend URL.
//
//	mage syncSpecFrom http://custom:9000
func SyncSpecFrom(url string) error {
	return syncSpecFrom(url)
}

func syncSpecFrom(url string) error {
	var baseURL string

	if url != "" {
		baseURL = url
		fmt.Printf("Using explicit backend: %s\n", baseURL)
	} else {
		var err error
		baseURL, err = probeBackend()
		if err != nil {
			return err
		}
		fmt.Printf("Auto-selected backend: %s\n", baseURL)
	}

	specURL := strings.TrimRight(baseURL, "/") + "/openapi.json"
	fmt.Printf("Fetching OpenAPI spec from %s …\n", specURL)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(specURL)
	if err != nil {
		return fmt.Errorf("fetch spec: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("fetch spec: HTTP %d: %s", resp.StatusCode, string(body)[:min(len(body), 200)])
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read spec body: %w", err)
	}

	var spec map[string]interface{}
	if err := json.Unmarshal(raw, &spec); err != nil {
		return fmt.Errorf("parse spec JSON: %w", err)
	}

	totalPaths := 0
	v2Paths := 0

	paths, ok := spec["paths"].(map[string]interface{})
	if ok {
		totalPaths = len(paths)
		filtered := make(map[string]interface{})
		for k, v := range paths {
			if strings.HasPrefix(k, "/api/v2") {
				filtered[k] = v
				v2Paths++
			}
		}
		spec["paths"] = filtered
	}

	pruneOrphanedSchemas(spec)
	downgradeToOpenAPI30(spec)
	deduplicateOperationIDs(spec)

	out, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal filtered spec: %w", err)
	}

	if err := os.MkdirAll("openapi", 0o755); err != nil {
		return fmt.Errorf("create openapi dir: %w", err)
	}
	if err := os.WriteFile("openapi/spec.json", append(out, '\n'), 0o644); err != nil {
		return fmt.Errorf("write spec: %w", err)
	}

	fmt.Printf("Wrote openapi/spec.json (%d v2 paths out of %d total)\n", v2Paths, totalPaths)
	return nil
}

// probeBackend tries known local ports and returns the first that responds.
func probeBackend() (string, error) {
	client := &http.Client{Timeout: 500 * time.Millisecond}

	for _, base := range localProbePorts {
		resp, err := client.Get(base + "/openapi.json")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return base, nil
			}
		}
	}
	return "", fmt.Errorf(
		"no running backend found (tried %v)\n\n"+
			"Start one with:\n"+
			"  cd ../stack && tilt up          # full stack on :8000\n"+
			"  cd ../backend && just tilt-setup # backend-only on :8010\n\n"+
			"Or pass an explicit URL: mage syncSpec http://host:port",
		localProbePorts,
	)
}

// pruneOrphanedSchemas removes schema definitions not reachable from any v2 path.
// It computes a transitive closure: any schema referenced (directly or transitively)
// by a v2 path operation is kept.
func pruneOrphanedSchemas(spec map[string]interface{}) {
	components, ok := spec["components"].(map[string]interface{})
	if !ok {
		return
	}
	schemas, ok := components["schemas"].(map[string]interface{})
	if !ok {
		return
	}

	reachable := make(map[string]bool)

	pathsJSON, _ := json.Marshal(spec["paths"])
	seedRefs := extractSchemaRefs(string(pathsJSON))
	for _, ref := range seedRefs {
		reachable[ref] = true
	}

	changed := true
	for changed {
		changed = false
		for name := range reachable {
			def, exists := schemas[name]
			if !exists {
				continue
			}
			defJSON, _ := json.Marshal(def)
			for _, ref := range extractSchemaRefs(string(defJSON)) {
				if !reachable[ref] {
					reachable[ref] = true
					changed = true
				}
			}
		}
	}

	kept := make(map[string]interface{})
	for name, def := range schemas {
		if reachable[name] {
			kept[name] = def
		}
	}

	removed := len(schemas) - len(kept)
	if removed > 0 {
		fmt.Printf("Pruned %d orphaned schemas (kept %d)\n", removed, len(kept))
	}
	components["schemas"] = kept
}

func extractSchemaRefs(text string) []string {
	var refs []string
	prefix := `"#/components/schemas/`
	for {
		idx := strings.Index(text, prefix)
		if idx < 0 {
			break
		}
		text = text[idx+len(prefix):]
		end := strings.Index(text, `"`)
		if end < 0 {
			break
		}
		refs = append(refs, text[:end])
		text = text[end:]
	}
	return refs
}

// downgradeToOpenAPI30 patches a 3.1 spec to be compatible with oapi-codegen
// (which only fully supports 3.0). It converts:
//   - anyOf: [{type: T}, {type: null}] → {type: T, nullable: true}
//   - openapi: "3.1.x" → "3.0.3"
func downgradeToOpenAPI30(spec map[string]interface{}) {
	if v, ok := spec["openapi"].(string); ok && strings.HasPrefix(v, "3.1") {
		spec["openapi"] = "3.0.3"
		fmt.Println("Downgraded OpenAPI version 3.1 → 3.0.3 for oapi-codegen compatibility")
	}
	patchNullableAnyOf(spec)
}

func patchNullableAnyOf(node interface{}) {
	switch n := node.(type) {
	case map[string]interface{}:
		if anyOf, ok := n["anyOf"].([]interface{}); ok {
			var nonNull []interface{}
			hasNull := false
			for _, item := range anyOf {
				if m, ok := item.(map[string]interface{}); ok {
					if t, _ := m["type"].(string); t == "null" {
						hasNull = true
						continue
					}
				}
				nonNull = append(nonNull, item)
			}
			if hasNull {
				n["nullable"] = true
				if len(nonNull) == 1 {
					if m, ok := nonNull[0].(map[string]interface{}); ok {
						for k, v := range m {
							n[k] = v
						}
					}
					delete(n, "anyOf")
				} else if len(nonNull) > 1 {
					n["anyOf"] = nonNull
				} else {
					delete(n, "anyOf")
				}
			}
		}
		for _, v := range n {
			patchNullableAnyOf(v)
		}
	case []interface{}:
		for _, v := range n {
			patchNullableAnyOf(v)
		}
	}
}

// deduplicateOperationIDs ensures every operationId is unique. If the backend
// spec has duplicate IDs (e.g. POST and PUT with the same operationId),
// the duplicates are renamed with a method suffix.
func deduplicateOperationIDs(spec map[string]interface{}) {
	paths, ok := spec["paths"].(map[string]interface{})
	if !ok {
		return
	}

	seen := make(map[string]string) // operationId → "METHOD path"
	dupes := 0

	for path, methods := range paths {
		methodMap, ok := methods.(map[string]interface{})
		if !ok {
			continue
		}
		for method, op := range methodMap {
			opMap, ok := op.(map[string]interface{})
			if !ok {
				continue
			}
			oid, _ := opMap["operationId"].(string)
			if oid == "" {
				continue
			}
			if prev, exists := seen[oid]; exists {
				newID := oid + "_" + strings.ToLower(method)
				opMap["operationId"] = newID
				dupes++
				fmt.Printf("Renamed duplicate operationId %q (%s %s, first seen at %s) → %s\n", oid, strings.ToUpper(method), path, prev, newID)
			} else {
				seen[oid] = strings.ToUpper(method) + " " + path
			}
		}
	}
	if dupes > 0 {
		fmt.Printf("Fixed %d duplicate operationId(s)\n", dupes)
	}
}

// ProcessSpec applies v2 filtering, 3.1→3.0 downgrade, schema pruning, and
// operationId dedup to an existing openapi/spec.json (e.g. one downloaded from
// a CI artifact). This is the same transform pipeline that SyncSpec runs after
// fetching from a live backend.
func ProcessSpec() error {
	const specPath = "openapi/spec.json"

	raw, err := os.ReadFile(specPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", specPath, err)
	}

	var spec map[string]interface{}
	if err := json.Unmarshal(raw, &spec); err != nil {
		return fmt.Errorf("parse %s: %w", specPath, err)
	}

	totalPaths := 0
	v2Paths := 0

	paths, ok := spec["paths"].(map[string]interface{})
	if ok {
		totalPaths = len(paths)
		filtered := make(map[string]interface{})
		for k, v := range paths {
			if strings.HasPrefix(k, "/api/v2") {
				filtered[k] = v
				v2Paths++
			}
		}
		spec["paths"] = filtered
	}

	pruneOrphanedSchemas(spec)
	downgradeToOpenAPI30(spec)
	deduplicateOperationIDs(spec)

	out, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal spec: %w", err)
	}

	if err := os.WriteFile(specPath, append(out, '\n'), 0o644); err != nil {
		return fmt.Errorf("write spec: %w", err)
	}

	fmt.Printf("Processed openapi/spec.json (%d v2 paths out of %d total)\n", v2Paths, totalPaths)
	return nil
}

// Generate runs oapi-codegen against openapi/spec.json to produce a typed Go client.
func Generate() error {
	if _, err := os.Stat("openapi/spec.json"); os.IsNotExist(err) {
		return fmt.Errorf("openapi/spec.json not found — run `mage syncSpec` first")
	}

	if !hasBinary("oapi-codegen") {
		fmt.Println("Installing oapi-codegen …")
		if err := sh.RunV("go", "install", "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest"); err != nil {
			return fmt.Errorf("install oapi-codegen: %w", err)
		}
	}

	fmt.Println("Generating typed client from openapi/spec.json …")
	if err := sh.RunV("oapi-codegen",
		"-package", "generated",
		"-generate", "types,client",
		"-o", "internal/api/generated/client.gen.go",
		"openapi/spec.json",
	); err != nil {
		return err
	}

	fmt.Println("Running go mod tidy …")
	if err := sh.RunV("go", "mod", "tidy"); err != nil {
		return err
	}

	fmt.Println("Done. Generated: internal/api/generated/client.gen.go")
	printGeneratedStats()
	return nil
}

func printGeneratedStats() {
	data, err := os.ReadFile("internal/api/generated/client.gen.go")
	if err != nil {
		return
	}
	lines := strings.Count(string(data), "\n")
	types := strings.Count(string(data), "type ")
	fmt.Printf("  %d lines, ~%d types\n", lines, types)
}

// Codegen runs SyncSpec then Generate (full pipeline).
func Codegen() error {
	if err := SyncSpec(); err != nil {
		return err
	}
	return Generate()
}

// Tidy runs go mod tidy.
func Tidy() error {
	return sh.RunV("go", "mod", "tidy")
}

// -----------------------------------------------------------------------------
// DEV SHORTCUTS
// -----------------------------------------------------------------------------

// Smoke builds the binary and runs quick sanity checks.
func Smoke() error {
	mg.Deps(Build)

	_, _ = loadDotEnvIfPresent(".env")

	for _, args := range [][]string{
		{"version"},
		{"config", "show"},
		{"config", "instances"},
	} {
		if err := sh.RunV("./"+bin, args...); err != nil {
			return err
		}
	}
	fmt.Println("\nSmoke tests passed.")
	return nil
}

// -----------------------------------------------------------------------------
// HELPERS
// -----------------------------------------------------------------------------

func gitOutputOr(fallback string, args ...string) string {
	out, err := sh.Output("git", args...)
	if err != nil {
		return fallback
	}
	return out
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func hasBinary(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func init() {
	os.Setenv("CGO_ENABLED", "0")
}
