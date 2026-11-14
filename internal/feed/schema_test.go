package feed

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/search"
)

// schemaObject is the subset of JSON Schema used by docs/feed.schema.json.
type schemaObject struct {
	Required   []string                   `json:"required"`
	Properties map[string]json.RawMessage `json:"properties"`
	Enum       []string                   `json:"enum"`
	Const      any                        `json:"const"`
}

type schemaDoc struct {
	schemaObject
	Defs map[string]schemaObject `json:"$defs"`
}

func loadSchema(t *testing.T) schemaDoc {
	t.Helper()
	b, err := os.ReadFile("../../docs/feed.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var s schemaDoc
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("feed.schema.json: %v", err)
	}
	return s
}

func prop(t *testing.T, o schemaObject, name string) schemaObject {
	t.Helper()
	var p schemaObject
	if err := json.Unmarshal(o.Properties[name], &p); err != nil {
		t.Fatalf("property %s: %v", name, err)
	}
	return p
}

// jsonFields maps each JSON field name of struct type t to whether it may
// be omitted (omitempty/omitzero).
func jsonFields(t reflect.Type) map[string]bool {
	out := map[string]bool{}
	for f := range t.Fields() {
		name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" || !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		out[name] = strings.Contains(opts, "omitempty") || strings.Contains(opts, "omitzero")
	}
	return out
}

// TestSchemaMatchesTypes keeps docs/feed.schema.json and the Go types in
// sync: every field the encoder can emit is declared, and every required
// field is always emitted.
func TestSchemaMatchesTypes(t *testing.T) {
	s := loadSchema(t)
	objects := []struct {
		name   string
		schema schemaObject
		typ    reflect.Type
	}{
		{"event", s.schemaObject, reflect.TypeFor[Event]()},
		{"source", s.Defs["source"], reflect.TypeFor[SourceEvent]()},
		{"run", s.Defs["run"], reflect.TypeFor[RunEvent]()},
		{"job", s.Defs["job"], reflect.TypeFor[model.Job]()},
	}
	for _, o := range objects {
		fields := jsonFields(o.typ)
		for name := range fields {
			if _, ok := o.schema.Properties[name]; !ok {
				t.Errorf("%s: field %q is emitted but not in the schema", o.name, name)
			}
		}
		for name := range o.schema.Properties {
			if _, ok := fields[name]; !ok {
				t.Errorf("%s: schema declares %q, which %s doesn't have", o.name, name, o.typ)
			}
		}
		for _, name := range o.schema.Required {
			if omit, ok := fields[name]; !ok || omit {
				t.Errorf("%s: %q is required by the schema but may be omitted", o.name, name)
			}
		}
	}
}

func TestSchemaEnums(t *testing.T) {
	s := loadSchema(t)
	job := s.Defs["job"]
	strs := func(xs ...string) []string { return xs }
	tests := []struct {
		name   string
		schema []string
		want   []string
	}{
		{"type", prop(t, s.schemaObject, "type").Enum, strs(string(TypeSource), string(TypeJob), string(TypeRun))},
		{"source.status", prop(t, s.Defs["source"], "status").Enum,
			strs(string(search.StatusOK), string(search.StatusCached), string(search.StatusSkipped), string(search.StatusError))},
		{"job.work_mode", prop(t, job, "work_mode").Enum, enumOf(model.AllWorkModes, model.ModeUnknown)},
		{"job.job_type", prop(t, job, "job_type").Enum, enumOf(model.AllJobTypes, model.TypeUnknown)},
		{"job.experience", prop(t, job, "experience").Enum, enumOf(model.AllExperience, model.ExpUnknown)},
		{"area", s.Defs["area"].Enum, enumOf(model.AllAreas)},
	}
	for _, tt := range tests {
		a, b := slices.Sorted(slices.Values(tt.schema)), slices.Sorted(slices.Values(tt.want))
		if !slices.Equal(a, b) {
			t.Errorf("%s enum = %v, want %v", tt.name, a, b)
		}
	}
	if v := prop(t, s.schemaObject, "v").Const; v != float64(Version) {
		t.Errorf("schema v const = %v, want %d", v, Version)
	}
}

func enumOf[T ~string](all []T, extra ...T) []string {
	var out []string
	for _, v := range slices.Concat(all, extra) {
		out = append(out, string(v))
	}
	return out
}

// TestGoldenEventsSatisfySchema checks the required fields on real encoder
// output, so the golden file and the schema can't drift apart.
func TestGoldenEventsSatisfySchema(t *testing.T) {
	s := loadSchema(t)
	b, err := os.ReadFile("testdata/batch.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var env map[string]any
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatal(err)
		}
		for _, k := range s.Required {
			if _, ok := env[k]; !ok {
				t.Errorf("line %d: missing %q", i+1, k)
			}
		}
		typ, _ := env["type"].(string)
		payload, ok := env[typ].(map[string]any)
		if !ok {
			t.Errorf("line %d: %s event without a %q object", i+1, typ, typ)
			continue
		}
		for _, k := range s.Defs[typ].Required {
			if _, ok := payload[k]; !ok {
				t.Errorf("line %d: %s.%s missing", i+1, typ, k)
			}
		}
	}
}
