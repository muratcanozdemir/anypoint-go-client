// Package jsonx reads values out of decoded, untyped JSON (any, map[string]any, []any).
package jsonx

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Dig walks a dotted path ("a.b.0.c") through decoded JSON. It returns nil when
// any step is missing or has the wrong type.
func Dig(v any, path string) any {
	if path == "" {
		return v
	}
	for k := range strings.SplitSeq(path, ".") {
		switch t := v.(type) {
		case map[string]any:
			v = t[k]
		case []any:
			i, err := strconv.Atoi(k)
			if err != nil || i < 0 || i >= len(t) {
				return nil
			}
			v = t[i]
		default:
			return nil
		}
	}
	return v
}

// Str renders a JSON scalar as text; arrays are comma-joined, objects re-encoded.
func Str(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	case []any:
		parts := make([]string, len(t))
		for i, e := range t {
			parts[i] = Str(e)
		}
		return strings.Join(parts, ",")
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

// List returns v as a JSON array, or nil.
func List(v any) []any {
	l, _ := v.([]any)
	return l
}

// Maps returns the objects of a JSON array; non-objects are skipped.
func Maps(v any) []map[string]any {
	var out []map[string]any
	for _, e := range List(v) {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// Items unwraps the common Anypoint list envelopes ({data:[]}, {items:[]}, …)
// or a bare array.
func Items(v any) []map[string]any {
	if _, ok := v.([]any); ok {
		return Maps(v)
	}
	for _, k := range []string{"data", "items", "values", "policies", "assets", "apis", "results"} {
		if l, ok := Dig(v, k).([]any); ok {
			return Maps(l)
		}
	}
	return nil
}
