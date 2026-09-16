package main

import (
	"log/slog"
	"testing"

	"tux-to-any/internal/ir"
)

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
