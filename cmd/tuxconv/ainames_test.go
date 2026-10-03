package main

import (
	"context"
	"log/slog"
	"testing"

	"tux-to-any/internal/budget"
	"tux-to-any/internal/flow"
	"tux-to-any/internal/ir"
)

// TestUniqueEndpointName pins the collision ladder shared by every naming
// seam: free base, token suffix, numeric fallback — and that every returned
// name is registered, so a rename can never be handed out again.
func TestUniqueEndpointName(t *testing.T) {
	tests := []struct {
		name  string
		base  string
		token string
		taken []string
		want  string
	}{
		{"free name keeps base", "GetFoo", "A", nil, "GetFoo"},
		{"collision gains the token", "GetFoo", "Bar", []string{"GetFoo"}, "GetFooBar"},
		{"token taken falls back to numeric", "GetFoo", "Bar", []string{"GetFoo", "GetFooBar"}, "GetFoo2"},
		{"numeric skips taken numbers", "GetFoo", "", []string{"GetFoo", "GetFoo2", "GetFoo3"}, "GetFoo4"},
		{"empty token gets the numeric suffix", "GetFoo", "", []string{"GetFoo"}, "GetFoo2"},
		{"empty name stays the TODO placeholder", "", "A", []string{""}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			taken := map[string]bool{}
			for _, n := range tt.taken {
				taken[n] = true
			}
			got := uniqueEndpointName(tt.base, tt.token, taken)
			if got != tt.want {
				t.Fatalf("uniqueEndpointName(%q, %q, %v) = %q, want %q", tt.base, tt.token, tt.taken, got, tt.want)
			}
			if got != "" && !taken[got] {
				t.Errorf("returned name %q not registered in taken", got)
			}
		})
	}
}

// TestDedupeEndpointNames pins the loader-legal guarantee over an ordered
// suggestion set — including the cascade the first cut missed: a renamed
// entry occupies its new name before later entries are processed, so an
// entry proposing that name must move on.
func TestDedupeEndpointNames(t *testing.T) {
	tests := []struct {
		name  string
		order []string
		in    map[string]aiSuggestion
		token map[string]string
		want  map[string]string
	}{
		{
			name:  "no collisions keeps every proposal",
			order: []string{"a", "b"},
			in:    map[string]aiSuggestion{"a": {Name: "GetA"}, "b": {Name: "GetB"}},
			token: map[string]string{"a": "1", "b": "2"},
			want:  map[string]string{"a": "GetA", "b": "GetB"},
		},
		{
			name:  "repeated names gain their tokens",
			order: []string{"a", "b"},
			in:    map[string]aiSuggestion{"a": {Name: "GetFoo"}, "b": {Name: "GetFoo"}},
			token: map[string]string{"a": "1", "b": "2"},
			want:  map[string]string{"a": "GetFoo", "b": "GetFoo2"},
		},
		{
			name:  "same token falls through to numeric",
			order: []string{"a", "b", "c"},
			in: map[string]aiSuggestion{
				"a": {Name: "GetFoo"}, "b": {Name: "GetFoo"}, "c": {Name: "GetFoo"},
			},
			token: map[string]string{"a": "1", "b": "1", "c": "1"},
			want:  map[string]string{"a": "GetFoo", "b": "GetFoo1", "c": "GetFoo2"},
		},
		{
			name:  "renames are registered before later entries",
			order: []string{"a", "b", "c"},
			in: map[string]aiSuggestion{
				"a": {Name: "GetFoo"}, "b": {Name: "GetFoo"}, "c": {Name: "GetFooBar"},
			},
			token: map[string]string{"a": "A", "b": "Bar", "c": "Baz"},
			want:  map[string]string{"a": "GetFoo", "b": "GetFooBar", "c": "GetFooBarBaz"},
		},
		{
			name:  "empty TODO placeholders stay empty",
			order: []string{"a", "b"},
			in:    map[string]aiSuggestion{"a": {Name: ""}, "b": {Name: ""}},
			token: map[string]string{"a": "A", "b": "B"},
			want:  map[string]string{"a": "", "b": ""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dedupeEndpointNames(tt.in, tt.order, func(k string) string { return tt.token[k] }, slog.Default())
			seen := map[string]string{}
			for _, k := range tt.order {
				got := tt.in[k].Name
				if got != tt.want[k] {
					t.Errorf("%s name = %q, want %q", k, got, tt.want[k])
				}
				if got == "" {
					continue
				}
				if prev, dup := seen[got]; dup {
					t.Errorf("name %q produced by %s and %s", got, prev, k)
				}
				seen[got] = k
			}
		})
	}
}

// TestAINameEndpointsDedupesDuplicateCensus pins the deterministic path:
// two candidates over the same cursor share a proposal, so the second gains
// its condition index instead of repeating the loader-illegal name.
func TestAINameEndpointsDedupesDuplicateCensus(t *testing.T) {
	candidates := []flow.Candidate{
		{Key: "c1", QueryIDs: []string{"cur_nav"}},
		{Key: "c2", QueryIDs: []string{"cur_nav"}},
		{Key: "c3", Adds: []string{"FML_MF_NAV_DATE"}},
	}
	out := aiNameEndpoints(context.Background(), slog.Default(), nil, budget.Budget{}, &ir.File{}, nil, candidates, nil, nil)
	want := map[string]string{"c1": "GetNav", "c2": "GetNav2", "c3": "GetMfNavDate"}
	for _, c := range candidates {
		sug := out[c.Key]
		if sug.Name != want[c.Key] {
			t.Errorf("%s name = %q, want %q", c.Key, sug.Name, want[c.Key])
		}
		if !sug.Deterministic {
			t.Errorf("%s must stay a deterministic suggestion", c.Key)
		}
	}
}

// TestAINameScenariosDedupesDuplicateCensus pins the scenario path: slices
// with identical census shapes propose one name; later slices gain the axis
// value, and a collision with an existing token falls through to numeric.
func TestAINameScenariosDedupesDuplicateCensus(t *testing.T) {
	scens := []*flow.Scenario{
		{Key: "c_flag=F", Var: "c_flag", Value: "F", Gets: []string{"FML_MF_NAV_DATE"}},
		{Key: "c_flag=H", Var: "c_flag", Value: "H", Gets: []string{"FML_MF_NAV_DATE"}},
		{Key: "c_flag=I", Var: "c_flag", Value: "I", Gets: []string{"FML_MF_NAV_DATE"}},
	}
	out := aiNameScenarios(context.Background(), slog.Default(), nil, budget.Budget{}, &ir.File{}, scens, nil, nil, nil)
	want := map[string]string{
		"c_flag=F": "GetMfNavDate",
		"c_flag=H": "GetMfNavDateH",
		"c_flag=I": "GetMfNavDateI",
	}
	seen := map[string]string{}
	for _, sc := range scens {
		got := out[sc.Key].Name
		if got != want[sc.Key] {
			t.Errorf("%s name = %q, want %q", sc.Key, got, want[sc.Key])
		}
		if prev, dup := seen[got]; dup {
			t.Errorf("name %q produced by %s and %s", got, prev, sc.Key)
		}
		seen[got] = sc.Key
	}
}

// TestDedupeRowNames pins the AI-naming guard: a suggested row name may be
// shared by identical shapes, but a name claimed for a different shape is
// cleared so the deterministic profile row name (unique per method) takes
// over — two structs under one name cannot compile.
func TestDedupeRowNames(t *testing.T) {
	queries := map[string]*ir.Query{
		"cur_a": {ID: "cur_a", RowShape: []string{"a", "b"}},
		"cur_b": {ID: "cur_b", RowShape: []string{"a", "b"}},
		"cur_c": {ID: "cur_c", RowShape: []string{"c"}},
	}
	sugs := map[string]aiSuggestion{
		"e1": {Methods: map[string]methodPinSuggestion{"cur_a": {Name: "GetA", Row: "SharedRow"}}},
		"e2": {Methods: map[string]methodPinSuggestion{"cur_b": {Name: "GetB", Row: "SharedRow"}}},
		"e3": {Methods: map[string]methodPinSuggestion{"cur_c": {Name: "GetC", Row: "SharedRow"}}},
	}
	dedupeRowNames(sugs, queries, slog.Default())
	if got := sugs["e1"].Methods["cur_a"].Row; got != "SharedRow" {
		t.Errorf("first claim lost: %q", got)
	}
	if got := sugs["e2"].Methods["cur_b"].Row; got != "SharedRow" {
		t.Errorf("identical shape must share the row: %q", got)
	}
	if got := sugs["e3"].Methods["cur_c"].Row; got != "" {
		t.Errorf("different shape kept a colliding row name: %q", got)
	}
}
