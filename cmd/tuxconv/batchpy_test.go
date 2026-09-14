package main

import (
	"strings"
	"testing"

	"tux-to-any/internal/pychk"
	"tux-to-any/internal/pygen"
	"tux-to-any/internal/sqlchk"
)

// The engine-wiring audit (docs/engine-wiring-audit.md Tier-1 #1/#11) fixed
// the batchpy silent-write pair: structural issues print, per-const fidelity
// detail prints and archives. This pins the report surface — every gate
// outcome the engine computes reaches the operator.
func TestWriteBatchModuleReportSurfacesEngineOutput(t *testing.T) {
	res := pygen.Result{
		Retention: pygen.Retention{
			Shape: "repo", DMLLoop: "batch", SelectTotal: 2, SelectMatched: 1,
			PyMode: "ast", SQLDeviations: 1, LogSitesTotal: 3, LogCallsEmitted: 2,
		},
		Notes:     []string{"llm fill failed: forced"},
		Structure: []pychk.Issue{{Line: 42, Msg: "bad indentation"}},
		Fidelity: []sqlchk.Result{
			{
				Method: "FETCH_A_QUERY", Status: sqlchk.StatusDeviated,
				Deviations: []sqlchk.Deviation{{Kind: sqlchk.DevBinds, Detail: "bind count differs"}},
			},
			{Method: "FETCH_B_QUERY", Status: sqlchk.StatusUnverifiable},
		},
		PyOK:     false,
		PyMode:   "ast",
		PyDetail: "line 9: invalid syntax",
	}
	var sb strings.Builder
	writeBatchModuleReport(&sb, "bat_demo", res, "python_out")
	out := sb.String()
	for _, want := range []string{
		"bat_demo: shape=repo dml=batch consts=2 phases=0 syntax=ast sql_deviations=1 retention=50% llm_calls=0 log_sites=2/3 -> python_out/bat_demo.py",
		"  note: llm fill failed: forced",
		"  structure: line 42: bad indentation",
		"  sql deviation: FETCH_A_QUERY [binds] bind count differs",
		"  sql unverifiable: FETCH_B_QUERY",
		"  python syntax: line 9: invalid syntax",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q\ngot:\n%s", want, out)
		}
	}
}
