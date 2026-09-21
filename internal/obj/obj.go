// Package obj reads fields out of decoded API JSON without a struct per schema.
//
// The CLI shows a handful of fields from each record and passes the rest
// through untouched, so declaring every schema as a Go type would be a second
// copy of the API that drifts from the first.
package obj

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

type Obj map[string]any

// Parse decodes a JSON object, keeping numbers exact.
func Parse(b []byte) (Obj, error) {
	var o Obj
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := d.Decode(&o); err != nil {
		return nil, err
	}
	return o, nil
}

// ParseAll decodes a list of raw items.
func ParseAll(raw []json.RawMessage) ([]Obj, error) {
	out := make([]Obj, 0, len(raw))
	for _, r := range raw {
		o, err := Parse(r)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}

// Get follows a dotted path such as "cost.recurring_monthly_minor".
func (o Obj) Get(path string) any {
	var cur any = map[string]any(o)
	for _, part := range strings.Split(path, ".") {
		m, ok := asMap(cur)
		if !ok {
			return nil
		}
		cur = m[part]
	}
	return cur
}

func asMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	case Obj:
		return m, true
	}
	return nil, false
}

// Str renders any scalar as text. A Message renders as its text, so callers
// never need to know which fields are Messages.
func (o Obj) Str(path string) string { return String(o.Get(path)) }

func String(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		if x {
			return "yes"
		}
		return "no"
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case map[string]any:
		if t, ok := x["text"].(string); ok {
			return t
		}
	case []any:
		parts := make([]string, 0, len(x))
		for _, e := range x {
			parts = append(parts, String(e))
		}
		return strings.Join(parts, ", ")
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func (o Obj) Int(path string) (int64, bool) {
	switch x := o.Get(path).(type) {
	case json.Number:
		n, err := x.Int64()
		return n, err == nil
	case float64:
		return int64(x), true
	}
	return 0, false
}

func (o Obj) Bool(path string) bool {
	b, _ := o.Get(path).(bool)
	return b
}

func (o Obj) Has(path string) bool { return o.Get(path) != nil }

func (o Obj) Obj(path string) Obj {
	m, _ := asMap(o.Get(path))
	return m
}

func (o Obj) List(path string) []Obj {
	arr, _ := o.Get(path).([]any)
	out := make([]Obj, 0, len(arr))
	for _, e := range arr {
		if m, ok := asMap(e); ok {
			out = append(out, m)
		}
	}
	return out
}

func (o Obj) Strings(path string) []string {
	arr, _ := o.Get(path).([]any)
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		out = append(out, String(e))
	}
	return out
}
