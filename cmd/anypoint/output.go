package main

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"text/tabwriter"

	"github.com/muratcanozdemir/anypoint-go-client/anypoint"
	"github.com/muratcanozdemir/anypoint-go-client/internal/jsonx"
)

// Short local names for the JSON helpers, used throughout the commands.
var (
	dig = jsonx.Dig
	str = jsonx.Str
)

// printJSON writes v indented. Models are written as the API returned them
// (anypoint.RawJSONer), including in slices.
func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(rawValue(v))
}

// rawValue replaces models, and slices of them, with their raw API JSON.
func rawValue(v any) any {
	if r, ok := v.(anypoint.RawJSONer); ok && r.RawJSON() != nil {
		return r.RawJSON()
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer && !rv.IsNil() {
		if r, ok := reflect.TypeAssert[anypoint.RawJSONer](rv.Elem()); ok && r.RawJSON() != nil {
			return r.RawJSON()
		}
	}
	if rv.Kind() != reflect.Slice || rv.Type().Elem().Kind() == reflect.Uint8 {
		return v
	}
	out := make([]any, rv.Len())
	for i := range out {
		out[i] = rawValue(rv.Index(i).Interface())
	}
	return out
}

// rows turns a slice of models or maps into JSON objects for printTable.
func rows(v any) []map[string]any {
	if m, ok := v.([]map[string]any); ok {
		return m
	}
	b, err := json.Marshal(rawValue(v))
	if err != nil {
		return nil
	}
	var out []map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

// object turns a model or map into a JSON object, for dig.
func object(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	b, err := json.Marshal(rawValue(v))
	if err != nil {
		return nil
	}
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

// Col is a table column: header plus one or more dotted paths (first non-empty wins).
type Col struct {
	Head  string
	Paths []string
}

func col(head string, paths ...string) Col { return Col{head, paths} }

func printTable(w io.Writer, data any, cols ...Col) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	heads := make([]string, len(cols))
	for i, c := range cols {
		heads[i] = c.Head
	}
	fmt.Fprintln(tw, strings.Join(heads, "\t"))
	for _, r := range rows(data) {
		cells := make([]string, len(cols))
		for i, c := range cols {
			for _, p := range c.Paths {
				if s := str(dig(r, p)); s != "" {
					cells[i] = strings.ReplaceAll(s, "\t", " ")
					break
				}
			}
		}
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	tw.Flush()
}

// printKV prints "key: value" pairs aligned, skipping empty values.
func printKV(w io.Writer, kv ...string) {
	tw := tabwriter.NewWriter(w, 0, 0, 1, ' ', 0)
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			fmt.Fprintf(tw, "%s:\t%s\n", kv[i], kv[i+1])
		}
	}
	tw.Flush()
}

// table writes aligned rows under a header.
type table struct{ tw *tabwriter.Writer }

func newTable(w io.Writer, heads ...string) *table {
	t := &table{tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)}
	t.row(heads...)
	return t
}

func (t *table) row(cells ...string) {
	for i, c := range cells {
		cells[i] = strings.ReplaceAll(c, "\t", " ")
	}
	fmt.Fprintln(t.tw, strings.Join(cells, "\t"))
}

func (t *table) flush() { _ = t.tw.Flush() }
