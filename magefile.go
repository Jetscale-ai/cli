//go:build mage

package main

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
)

var Default = Build

const (
	module             = "github.com/Jetscale-ai/cli"
	bin                = "bin/jetscale"
	localBinary        = "jetscale-local"
	oapiCodegenVersion = "v2.6.0"
	oapiCodegenModule  = "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen"
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

// BuildLocal builds bin/jetscale-local offline from the current sources and schema.
// It neither syncs the schema, generates code, installs tools, nor reads .env.
func BuildLocal() error {
	flags, err := localLDFlags()
	if err != nil {
		return err
	}
	if err := regularDirectory("bin"); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("bin", ".local-build-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	source := filepath.Join(tmp, localBinary)
	fmt.Fprintln(os.Stderr, "Building bin/jetscale-local (offline)")
	if err := sh.RunWithV(map[string]string{
		"GOPROXY": "off", "GOSUMDB": "off", "GOTOOLCHAIN": "local",
	}, "go", "build", "-mod=readonly", "-ldflags", flags, "-o", source, "./cmd/jetscale"); err != nil {
		return err
	}
	return installLocalFile(source, "bin")
}

// InstallLocal rebuilds and atomically installs jetscale-local in ~/.local/bin.
// Only an explicit invocation installs; JETSCALE_LOCAL_BIN_DIR overrides the directory.
func InstallLocal() error {
	dir := os.Getenv("JETSCALE_LOCAL_BIN_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		dir = filepath.Join(home, ".local", "bin")
	}
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("JETSCALE_LOCAL_BIN_DIR must be an absolute user-owned directory")
	}
	if err := BuildLocal(); err != nil {
		return err
	}
	if err := installLocalFile(filepath.Join("bin", localBinary), dir); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Installed", filepath.Join(dir, localBinary))
	return nil
}

func localLDFlags() (string, error) {
	raw, err := os.ReadFile("openapi/spec.json")
	if err != nil {
		return "", fmt.Errorf("read local schema (sync explicitly if needed): %w", err)
	}
	var spec struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		return "", fmt.Errorf("parse local schema: %w", err)
	}
	revision, err := localGit("rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	status, err := localGit("status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return "", err
	}
	sourceHash, err := localSourceHash()
	if err != nil {
		return "", err
	}
	dirty := "false"
	if status != "" {
		dirty = "true"
	}
	values := []struct{ name, value string }{
		{"cmd.version", "local"},
		{"cmd.commit", revision},
		{"cmd.date", time.Now().UTC().Format(time.RFC3339)},
		{"buildinfo.Channel", "local"},
		{"buildinfo.BackendSHA", envOrDefault("JETSCALE_LOCAL_BACKEND_SHA", "unknown")},
		{"buildinfo.SchemaSHA256", fmt.Sprintf("%x", sha256.Sum256(raw))},
		{"buildinfo.APIVersion", spec.Info.Version},
		{"buildinfo.SourceSHA256", sourceHash},
		{"buildinfo.SourceDirty", dirty},
	}
	flags := []string{"-s", "-w"}
	for _, field := range values {
		value := field.value
		if value == "" {
			value = "unknown"
		}
		// The Go linker parses this string again; reject quoting/control characters.
		if strings.ContainsAny(value, "'\"\r\n\t") {
			return "", fmt.Errorf("invalid local build metadata for %s", field.name)
		}
		flags = append(flags, fmt.Sprintf("-X '%s/internal/%s=%s'", module, field.name, value))
	}
	return strings.Join(flags, " "), nil
}

func localGit(args ...string) (string, error) {
	return sh.Output("git", append([]string{"--no-pager", "--no-optional-locks"}, args...)...)
}

// Hash current Go inputs, including untracked sources, rather than claiming a
// dirty checkout is fully identified by HEAD. Non-source files are not hashed.
func localSourceHash() (string, error) {
	files, err := localGit("ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", "*.go", "go.mod", "go.sum")
	if err != nil {
		return "", err
	}
	paths := strings.Split(files, "\x00")
	sort.Strings(paths)
	hash := sha256.New()
	previous := ""
	for _, path := range paths {
		if path == "" || path == previous {
			continue
		}
		previous = path
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue // Tracked deletions are absent from the build too.
		}
		if err != nil {
			return "", err
		}
		_, _ = fmt.Fprintf(hash, "%d:%s%d:", len(path), path, len(data))
		hash.Write(data)
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// Refuse symlink directory components instead of following a user-bin alias.
func regularDirectory(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	parent := filepath.Dir(abs)
	if parent != abs {
		if err := regularDirectory(parent); err != nil {
			return err
		}
	}
	info, err := os.Lstat(abs)
	if os.IsNotExist(err) {
		return os.Mkdir(abs, 0o755)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing non-directory or symlink install path: %s", abs)
	}
	return nil
}

// The fixed basename cannot replace jetscale. Rename replaces a directory entry,
// not its inode, so even an existing hard link to the live binary stays untouched.
func installLocalFile(source, dir string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("local binary source must be a regular file")
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	opened, err := in.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(info, opened) {
		return fmt.Errorf("local binary source changed while opening")
	}
	if err := regularDirectory(dir); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	checkTarget := func() error {
		info, err := root.Lstat(localBinary)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to replace non-regular file %s", filepath.Join(dir, localBinary))
		}
		return nil
	}
	if err := checkTarget(); err != nil {
		return err
	}
	temp := ".jetscale-local-" + rand.Text()
	out, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(temp) }()
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Chmod(0o755); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := checkTarget(); err != nil {
		return err
	}
	return root.Rename(temp, localBinary)
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

	client, err := specHTTPClient(os.Getenv("JETSCALE_LOCAL_CA_FILE"))
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
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
	patchOptionalDiscriminatorPointers(spec)
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

// specHTTPClient extends system trust; certificate and hostname checks stay on.
func specHTTPClient(caFile string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read JETSCALE_LOCAL_CA_FILE: %w", err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("load system certificate trust: %w", err)
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("JETSCALE_LOCAL_CA_FILE contains no CA certificates")
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	}
	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return fmt.Errorf("spec redirect refused; pass the final backend URL explicitly")
		},
	}, nil
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
			"Or pass an explicit URL: mage syncSpecFrom http://host:port",
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

// patchOptionalDiscriminatorPointers works around oapi-codegen v2.6.0's union
// helpers assigning string literals to optional *string discriminators. This is
// trusted codegen metadata, not a change to required/default/const or omitempty.
// Restrict it to non-empty string constants: an empty string is not a valid
// explicit value there, so using the Go zero value for omission loses no valid value.
func patchOptionalDiscriminatorPointers(spec map[string]interface{}) {
	components, _ := spec["components"].(map[string]interface{})
	schemas, _ := components["schemas"].(map[string]interface{})
	var visit func(map[string]interface{})
	visit = func(schema map[string]interface{}) {
		discriminator, _ := schema["discriminator"].(map[string]interface{})
		propertyName, _ := discriminator["propertyName"].(string)
		for _, keyword := range []string{"oneOf", "anyOf", "allOf"} {
			variants, _ := schema[keyword].([]interface{})
			for _, value := range variants {
				variant, _ := value.(map[string]interface{})
				ref, _ := variant["$ref"].(string)
				const prefix = "#/components/schemas/"
				if propertyName != "" && keyword != "allOf" && strings.HasPrefix(ref, prefix) {
					name := strings.TrimPrefix(ref, prefix)
					// Resolve only direct local components, not arbitrary JSON pointers.
					if !strings.Contains(name, "/") {
						name = strings.NewReplacer("~1", "/", "~0", "~").Replace(name)
						target, _ := schemas[name].(map[string]interface{})
						properties, _ := target["properties"].(map[string]interface{})
						property, _ := properties[propertyName].(map[string]interface{})
						required, _ := target["required"].([]interface{})
						optional := true
						for _, field := range required {
							if field == propertyName {
								optional = false
							}
						}
						literal, _ := property["const"].(string)
						if optional && literal != "" && property["type"] == "string" && property["nullable"] != true && property["$ref"] == nil {
							property["x-go-type-skip-optional-pointer"] = true
						}
					}
				}
				visit(variant)
			}
		}
		// Traverse schema positions only; examples/defaults are application data.
		properties, _ := schema["properties"].(map[string]interface{})
		for _, value := range properties {
			child, _ := value.(map[string]interface{})
			visit(child)
		}
		for _, keyword := range []string{"items", "additionalProperties", "not"} {
			if child, ok := schema[keyword].(map[string]interface{}); ok {
				visit(child)
			}
		}
	}
	for _, value := range schemas {
		schema, _ := value.(map[string]interface{})
		visit(schema)
	}
}

// deduplicateOperationIDs preserves the first ID in sorted path/method order.
// Duplicates get a method suffix, then a numeric suffix starting at 2 if needed.
func deduplicateOperationIDs(spec map[string]interface{}) {
	paths, _ := spec["paths"].(map[string]interface{})
	type operation struct {
		path, method, id string
		schema           map[string]interface{}
	}
	var operations []operation
	reserved := make(map[string]bool)
	for path, methods := range paths {
		methodMap, _ := methods.(map[string]interface{})
		for method, op := range methodMap {
			opMap, _ := op.(map[string]interface{})
			id, _ := opMap["operationId"].(string)
			if id != "" {
				operations = append(operations, operation{path, method, id, opMap})
				// Reserve all original IDs, including ones encountered later in order.
				reserved[id] = true
			}
		}
	}
	sort.Slice(operations, func(i, j int) bool {
		if operations[i].path != operations[j].path {
			return operations[i].path < operations[j].path
		}
		return operations[i].method < operations[j].method
	})

	seen := make(map[string]string) // original operationId → "METHOD path"
	dupes := 0
	for _, op := range operations {
		if prev, exists := seen[op.id]; exists {
			base := op.id + "_" + strings.ToLower(op.method)
			newID := base
			for suffix := 2; reserved[newID]; suffix++ {
				newID = fmt.Sprintf("%s_%d", base, suffix)
			}
			reserved[newID] = true
			op.schema["operationId"] = newID
			dupes++
			fmt.Printf("Renamed duplicate operationId %q (%s %s, first seen at %s) → %s\n", op.id, strings.ToUpper(op.method), op.path, prev, newID)
		} else {
			seen[op.id] = strings.ToUpper(op.method) + " " + op.path
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
	patchOptionalDiscriminatorPointers(spec)
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

// GenerateClient runs pinned oapi-codegen without tidying module dependencies.
func GenerateClient() error {
	if _, err := os.Stat("openapi/spec.json"); os.IsNotExist(err) {
		return fmt.Errorf("openapi/spec.json not found — run `mage syncSpec` first")
	}

	fmt.Fprintln(os.Stderr, "Generating typed client with oapi-codegen", oapiCodegenVersion)
	return sh.RunV("go", "run", oapiCodegenModule+"@"+oapiCodegenVersion,
		"-package", "generated",
		"-generate", "types,client",
		"-o", "internal/api/generated/client.gen.go",
		"openapi/spec.json",
	)
}

// Generate generates the typed client and tidies module dependencies.
func Generate() error {
	if err := GenerateClient(); err != nil {
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
	out, err := localGit(args...)
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

func init() {
	os.Setenv("CGO_ENABLED", "0")
}
