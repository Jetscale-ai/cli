package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"gopkg.in/yaml.v3"
)

// Format represents a supported output format.
type Format string

const (
	Table Format = "table"
	JSON  Format = "json"
	YAML  Format = "yaml"
)

// ParseFormat validates a --output flag value.
func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(s) {
	case "table", "":
		return Table, nil
	case "json":
		return JSON, nil
	case "yaml", "yml":
		return YAML, nil
	default:
		return "", fmt.Errorf("unknown output format %q (table|json|yaml)", s)
	}
}

// Column defines one column in table output.
type Column struct {
	Header string
	Field  func(row interface{}) string
}

// Printer renders structured data in the requested format.
type Printer struct {
	Format Format
	Out    io.Writer
}

// Print outputs the data. In table mode it uses the provided columns;
// in JSON/YAML mode it serialises the data directly, ignoring columns.
func (p Printer) Print(data interface{}, columns []Column) error {
	switch p.Format {
	case JSON:
		return p.printJSON(data)
	case YAML:
		return p.printYAML(data)
	default:
		return p.printTable(data, columns)
	}
}

// PrintRaw outputs pre-decoded JSON bytes in the requested format.
// Table mode falls back to pretty JSON since raw API responses don't have columns.
func (p Printer) PrintRaw(body []byte) error {
	switch p.Format {
	case YAML:
		var obj interface{}
		if err := json.Unmarshal(body, &obj); err != nil {
			_, wErr := p.Out.Write(body)
			return wErr
		}
		return yaml.NewEncoder(p.Out).Encode(obj)
	default:
		var pretty json.RawMessage
		if json.Unmarshal(body, &pretty) == nil {
			enc := json.NewEncoder(p.Out)
			enc.SetIndent("", "  ")
			return enc.Encode(pretty)
		}
		_, err := p.Out.Write(body)
		return err
	}
}

func (p Printer) printJSON(data interface{}) error {
	enc := json.NewEncoder(p.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}

func (p Printer) printYAML(data interface{}) error {
	return yaml.NewEncoder(p.Out).Encode(data)
}

func (p Printer) printTable(data interface{}, columns []Column) error {
	rows, ok := toSlice(data)
	if !ok {
		rows = []interface{}{data}
	}

	if len(columns) == 0 || len(rows) == 0 {
		return nil
	}

	w := tabwriter.NewWriter(p.Out, 0, 4, 2, ' ', 0)

	headers := make([]string, len(columns))
	for i, c := range columns {
		headers[i] = strings.ToUpper(c.Header)
	}
	fmt.Fprintln(w, strings.Join(headers, "\t"))

	for _, row := range rows {
		vals := make([]string, len(columns))
		for i, c := range columns {
			vals[i] = c.Field(row)
		}
		fmt.Fprintln(w, strings.Join(vals, "\t"))
	}

	return w.Flush()
}

func toSlice(v interface{}) ([]interface{}, bool) {
	switch s := v.(type) {
	case []interface{}:
		return s, true
	default:
		return nil, false
	}
}
