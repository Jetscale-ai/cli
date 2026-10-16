package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Jetscale-ai/cli/internal/api"
	"github.com/spf13/cobra"
)

const apiPageSize = 100

func newAPICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "api <path>",
		Short: "Make an authenticated request to the Jetscale API",
		Long: `Make an authenticated request to the Jetscale API and print the response.

The path is relative to /api/v2/ unless it starts with /api/. As with gh api,
the default method is GET, or POST when fields or --input are given; pass
-X GET to send fields as query parameters. --paginate always uses GET.

  -f key=value   string field
  -F key=value   typed field: true, false, null and numbers keep their type

--paginate follows api_offset pages of a GET collection and prints one JSON
array of every item. Requests are sent once and never retried; a non-GET
request that fails in transit exits 1 with reason outcome_uncertain. A non-2xx
reply is printed as received and exits by the usual codes.`,
		Args: exactArgs(1),
		Example: `  jetscale api auth/me
  jetscale api -X GET organization/companies -f slug="eq unstable-jetscale"
  jetscale api cloud/cloud-accounts --paginate -f company_id=8cb59069-340c-419e-8d1f-251404a4ba54
  jetscale api -X PATCH system/feature-flags/root/action/set/CLOUD.FOCUS -F value=true
  jetscale api -X POST organization/business-units --input unit.json`,
		RunE: runAPI,
	}
	cmd.Flags().StringP("method", "X", "", "HTTP method (default GET, or POST with fields or --input)")
	cmd.Flags().StringArrayP("raw-field", "f", nil, "string field key=value (repeatable)")
	cmd.Flags().StringArrayP("field", "F", nil, "typed field key=value (repeatable)")
	cmd.Flags().String("input", "", "file with the JSON request body (- for stdin)")
	cmd.Flags().Bool("paginate", false, "fetch every page of a GET collection")
	cmd.Flags().Bool("require-superuser", false, "refuse to run (exit 5) unless the signed-in user is an active superuser")
	cmd.Flags().Duration("timeout", 2*time.Minute, "overall deadline for the command (max 30m)")
	return cmd
}

func apiPath(raw string) (string, url.Values, error) {
	if strings.Contains(raw, "://") || strings.HasPrefix(raw, "//") {
		return "", nil, errors.New("give a path, not a URL; the instance comes from -i or --api-url")
	}
	if !strings.HasPrefix(raw, "/") {
		raw = "/api/v2/" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || !strings.HasPrefix(parsed.Path, "/api/") || strings.Contains(parsed.Path, "..") {
		return "", nil, errors.New("path must be under /api/")
	}
	return parsed.Path, parsed.Query(), nil
}

func typedValue(raw string) any {
	switch raw {
	case "true":
		return true
	case "false":
		return false
	case "null":
		return nil
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(raw, 64); err == nil {
		return f
	}
	return raw
}

func apiFields(cmd *cobra.Command) (map[string]any, error) {
	fields := map[string]any{}
	add := func(name string, typed bool) error {
		values, _ := cmd.Flags().GetStringArray(name)
		for _, pair := range values {
			key, value, ok := strings.Cut(pair, "=")
			if !ok || key == "" {
				return usageError(fmt.Errorf("-%s expects key=value, got %q", map[bool]string{true: "F", false: "f"}[typed], pair))
			}
			if typed {
				fields[key] = typedValue(value)
			} else {
				fields[key] = value
			}
		}
		return nil
	}
	if err := add("raw-field", false); err != nil {
		return nil, err
	}
	if err := add("field", true); err != nil {
		return nil, err
	}
	return fields, nil
}

func apiInput(cmd *cobra.Command) ([]byte, error) {
	path := flagString(cmd, "input")
	if path == "" {
		return nil, nil
	}
	var reader io.Reader
	if path == "-" {
		if flagPasswordStdin {
			return nil, usageError(errors.New("--input - cannot share stdin with --password-stdin"))
		}
		reader = cmd.InOrStdin()
	} else {
		file, err := os.Open(path)
		if err != nil {
			return nil, usageError(errors.New("--input file is not readable"))
		}
		defer func() { _ = file.Close() }()
		reader = file
	}
	body, err := io.ReadAll(io.LimitReader(reader, 4<<20+1))
	if err != nil || len(body) > 4<<20 || !json.Valid(body) {
		return nil, usageError(errors.New("--input must be JSON under 4 MiB"))
	}
	return body, nil
}

func runAPI(cmd *cobra.Command, args []string) error {
	path, query, err := apiPath(args[0])
	if err != nil {
		return usageError(err)
	}
	fields, err := apiFields(cmd)
	if err != nil {
		return err
	}
	input, err := apiInput(cmd)
	if err != nil {
		return err
	}
	if input != nil && len(fields) > 0 {
		return usageError(errors.New("use fields or --input, not both"))
	}
	paginate := flagBool(cmd, "paginate")
	method := strings.ToUpper(flagString(cmd, "method"))
	if method == "" {
		method = http.MethodGet
		if !paginate && (input != nil || len(fields) > 0) {
			method = http.MethodPost
		}
	}
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return usageError(fmt.Errorf("unsupported method %q", method))
	}
	if paginate && method != http.MethodGet {
		return usageError(errors.New("--paginate works only with GET"))
	}
	var body []byte
	if method == http.MethodGet {
		if input != nil {
			return usageError(errors.New("GET takes fields as query parameters, not --input"))
		}
		for key, value := range fields {
			query.Set(key, fmt.Sprint(value))
		}
	} else if input != nil {
		body = input
	} else if len(fields) > 0 {
		body, _ = json.Marshal(fields)
	}
	run, err := startResource(cmd)
	if err != nil {
		return err
	}
	defer run.cancel()
	if !paginate {
		reply, err := run.client.Raw(run.ctx, method, withQuery(path, query), body)
		if err != nil {
			return apiExit(err)
		}
		return run.printReply(method, reply)
	}
	items := []json.RawMessage{}
	for page := 0; page < 20; page++ {
		query.Set("api_limit", strconv.Itoa(apiPageSize))
		query.Set("api_offset", strconv.Itoa(page*apiPageSize))
		reply, err := run.client.Raw(run.ctx, method, withQuery(path, query), nil)
		if err != nil {
			return apiExit(err)
		}
		if reply.Status < 200 || reply.Status >= 300 {
			return run.printReply(method, reply)
		}
		var envelope struct {
			Data []json.RawMessage `json:"data"`
			Meta struct {
				Total *int `json:"total_count"`
			} `json:"meta"`
		}
		if json.Unmarshal(reply.Body, &envelope) != nil || envelope.Data == nil {
			return &ExitError{Code: ExitFailure, Reason: "invalid_response", Err: errors.New("--paginate needs a {\"data\": [...]} collection")}
		}
		items = append(items, envelope.Data...)
		if len(envelope.Data) < apiPageSize || (envelope.Meta.Total != nil && len(items) >= *envelope.Meta.Total) {
			merged, _ := json.Marshal(items)
			return run.out.PrintRaw(merged)
		}
	}
	return &ExitError{Code: ExitFailure, Reason: "page_bound", Err: errors.New("collection exceeds 20 pages; narrow it with fields")}
}

func withQuery(path string, query url.Values) string {
	if encoded := query.Encode(); encoded != "" {
		return path + "?" + encoded
	}
	return path
}

func (r *resourceRun) printReply(method string, reply api.RawResponse) error {
	if len(reply.Body) > 0 {
		if err := r.out.PrintRaw(reply.Body); err != nil {
			return err
		}
	}
	if reply.Status >= 200 && reply.Status < 300 {
		return nil
	}
	if method != http.MethodGet && (reply.Status == http.StatusBadGateway || reply.Status == http.StatusGatewayTimeout) {
		return &ExitError{Code: ExitFailure, Reason: "outcome_uncertain", Printed: true, Err: fmt.Errorf("HTTP %d after sending %s; the request may have been applied", reply.Status, method)}
	}
	mapped := apiExit(api.ResponseError(reply.Status, reply.Body))
	var exit *ExitError
	if errors.As(mapped, &exit) {
		exit.Printed = true
	}
	return mapped
}
