package main

import (
	"bytes"
	"testing"

	"tux-to-any/internal/convert"
	"tux-to-any/internal/ledger"
	"tux-to-any/internal/validate"
)

// The engine-wiring audit (docs/engine-wiring-audit.md Tier-1 #6) pinned
// the Tier-B surfacing: a failed batched go build/vet/test must print its
// trimmed errors — pre-fix the summary printed only the one-line Summary
// and the Errors slice was computed and dropped.
func TestPrintServiceSummarySurfacesTierBErrors(t *testing.T) {
	led := &ledger.Ledger{}
	res := &convert.Result{
		TierB: &validate.Result{
			Tier:    "compile",
			OK:      false,
			Errors:  []string{"services/nav.go:9:2: undefined: Foo", "go build: exit status 1"},
			Summary: "go build failed",
		},
	}
	var out bytes.Buffer
	printServiceSummary(&out, "nav", res, led, "gen/", "")
	text := out.String()
	if !bytes.Contains([]byte(text), []byte("tier B: go build failed")) {
		t.Errorf("summary line missing, got:\n%s", text)
	}
	for _, want := range res.TierB.Errors {
		if !bytes.Contains([]byte(text), []byte("tier B error: "+want)) {
			t.Errorf("tier B error %q must surface in the summary, got:\n%s", want, text)
		}
	}
}
