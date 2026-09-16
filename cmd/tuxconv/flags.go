package main

// commandFlags is the single home for reorderArgs' value-flag knowledge.
//
// Before: main.go:deriveValueFlags re-registered every subcommand's FlagSet
// into throwaway sets and sniffed DefValue != "false"/"true" to detect value
// flags (~70L mirror + fragile string sniffing). Any flag rename needed 2
// edits or reorderArgs silently mis-split; `ainames` was missing entirely so
// `-mapping <file>` space-form broke for that subcommand only.
//
// After: one declarative table. deriveValueFlags() in main.go builds from
// this. When adding a flag, add it here AND to the subcommand's FlagSet.
// Keep names in sync — the table is the contract reorderArgs relies on.
var commandFlags = map[string][]string{
	// value flags only (bool flags omitted — they never consume next arg)
	"extract":        {"out", "config"},
	"plan":           {"mapping", "config", "ledger"},
	"convertgo":      {"mapping", "config", "base"},
	"discover":       {"out", "config", "target"},
	"convertbatchpy": {"out", "config", "shape", "dml-loop"},
	"convertcs":      {"out", "mapping", "config"},
	"analyze":        {"csv", "weights", "pattern"},
	"gentest":        {"layers", "base", "config"},
	"ainames":        {"mapping", "config"},
}
