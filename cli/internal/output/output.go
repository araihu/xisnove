package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"go.yaml.in/yaml/v3"
)

type Format string

const (
	TableFormat Format = "table"
	JSONFormat  Format = "json"
	YAMLFormat  Format = "yaml"
)

type Table struct {
	Headers []string
	Rows    [][]string
}

type Renderer struct {
	Writer io.Writer
	Format Format
}

func (r Renderer) Render(value any, table Table) error {
	if r.Writer == nil {
		return errors.New("output writer is required")
	}
	switch r.Format {
	case "", TableFormat:
		return renderTable(r.Writer, table)
	case JSONFormat:
		encoder := json.NewEncoder(r.Writer)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(value); err != nil {
			return fmt.Errorf("encode JSON output: %w", err)
		}
		return nil
	case YAMLFormat:
		data, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("normalize YAML output: %w", err)
		}
		var normalized any
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		if err := decoder.Decode(&normalized); err != nil {
			return fmt.Errorf("normalize YAML output: %w", err)
		}
		normalized, err = normalizeYAMLNumbers(normalized)
		if err != nil {
			return fmt.Errorf("normalize YAML numbers: %w", err)
		}
		encoder := yaml.NewEncoder(r.Writer)
		encoder.SetIndent(4)
		if err := encoder.Encode(normalized); err != nil {
			return fmt.Errorf("encode YAML output: %w", err)
		}
		if err := encoder.Close(); err != nil {
			return fmt.Errorf("close YAML output: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported output format %q; use table, json, or yaml", r.Format)
	}
}

func normalizeYAMLNumbers(value any) (any, error) {
	switch typed := value.(type) {
	case json.Number:
		text := typed.String()
		if !strings.ContainsAny(text, ".eE") {
			if signed, err := strconv.ParseInt(text, 10, 64); err == nil {
				return signed, nil
			}
			if unsigned, err := strconv.ParseUint(text, 10, 64); err == nil {
				return unsigned, nil
			}
		}
		floating, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return nil, err
		}
		return floating, nil
	case []any:
		for i := range typed {
			normalized, err := normalizeYAMLNumbers(typed[i])
			if err != nil {
				return nil, err
			}
			typed[i] = normalized
		}
		return typed, nil
	case map[string]any:
		for key, item := range typed {
			normalized, err := normalizeYAMLNumbers(item)
			if err != nil {
				return nil, err
			}
			typed[key] = normalized
		}
		return typed, nil
	default:
		return value, nil
	}
}

func renderTable(writer io.Writer, table Table) error {
	if len(table.Headers) == 0 {
		return errors.New("table headers are required")
	}
	rows := make([][]string, len(table.Rows))
	for i, row := range table.Rows {
		if len(row) != len(table.Headers) {
			return fmt.Errorf("table row %d has %d cells; want %d", i, len(row), len(table.Headers))
		}
		rows[i] = append([]string(nil), row...)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return strings.Join(rows[i], "\x00") < strings.Join(rows[j], "\x00")
	})
	tw := tabwriter.NewWriter(writer, 0, 8, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, strings.Join(table.Headers, "\t")); err != nil {
		return fmt.Errorf("write table header: %w", err)
	}
	for _, row := range rows {
		if _, err := fmt.Fprintln(tw, strings.Join(row, "\t")); err != nil {
			return fmt.Errorf("write table row: %w", err)
		}
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("flush table output: %w", err)
	}
	return nil
}
