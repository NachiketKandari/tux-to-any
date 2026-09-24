package analyzer

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	scanner "tux-to-any/internal/tsscan"
)

// tpDynamicService names a tpcall/tpacall site whose target service is not a
// string literal (a variable or a wider expression) and is therefore
// unresolvable to a file by construction.
const tpDynamicService = "(dynamic)"

// TpSvcDep is one outbound Tuxedo service dependency of a file: every
// tpcall/tpacall site grouped by target service name. Kind is "tpcall" when
// every site is sync, "tpacall" when every site is async, else "both".
// Resolution (File/Resolved/Score/Complexity) needs the scanned tree, so it
// is filled by resolveTpDeps in directory mode; single-file reports keep the
// skeleton (Resolved=false) — the dependency is counted, never guessed.
type TpSvcDep struct {
	Service    string
	Kind       string
	Calls      int
	File       string
	Resolved   bool
	Score      int
	Complexity string
}

// tpDepSkeletons groups one file's tpcall/tpacall sites by target service.
// The first top-level argument is the service (mirroring ir.buildTPCalls);
// a non-literal first argument aggregates under tpDynamicService.
func tpDepSkeletons(facts *scanner.SourceFacts) []TpSvcDep {
	byService := map[string]*TpSvcDep{}
	for i := range facts.Calls {
		c := &facts.Calls[i]
		if !c.IsTpCall {
			continue
		}
		svc := tpCallService(c)
		if svc == "" {
			svc = tpDynamicService
		}
		d, ok := byService[svc]
		if !ok {
			d = &TpSvcDep{Service: svc}
			byService[svc] = d
		}
		d.Calls++
		kind := "tpcall"
		if c.Name == "tpacall" {
			kind = "tpacall"
		}
		switch {
		case d.Calls == 1:
			d.Kind = kind
		case d.Kind != kind:
			d.Kind = "both"
		}
	}
	deps := make([]TpSvcDep, 0, len(byService))
	for _, d := range byService {
		deps = append(deps, *d)
	}
	sort.Slice(deps, func(i, j int) bool {
		if deps[i].Service != deps[j].Service {
			return deps[i].Service < deps[j].Service
		}
		return deps[i].Kind < deps[j].Kind
	})
	return deps
}

// tpCallService returns the unquoted target service of a tpcall/tpacall
// site, or "" when the first argument is not a string literal.
func tpCallService(c *scanner.FunctionCall) string {
	args := splitTopArgs(c.Args)
	if len(args) == 0 {
		return ""
	}
	return unquoteLit(args[0])
}

// splitTopArgs splits raw call-argument text on top-level commas: commas
// inside quotes or nested parens/brackets never split.
func splitTopArgs(args string) []string {
	var out []string
	depth := 0
	quote := byte(0)
	start := 0
	for i := 0; i < len(args); i++ {
		ch := args[i]
		if quote != 0 {
			if ch == '\\' && quote == '"' {
				i++
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}
		switch ch {
		case '\'', '"':
			quote = ch
		case '(', '[':
			depth++
		case ')', ']':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				out = append(out, args[start:i])
				start = i + 1
			}
		}
	}
	if start <= len(args) {
		out = append(out, args[start:])
	}
	return out
}

// unquoteLit returns the contents of a double-quoted string literal, else "".
func unquoteLit(arg string) string {
	s := strings.TrimSpace(arg)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return ""
}

// svcStem returns the match key of a scanned file: its lowercased,
// extension-free base name (SVC_FOO.pc → svc_foo).
func svcStem(path string) string {
	return strings.ToLower(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
}

// resolveTpDeps fills the resolution half of every report's TpSvcDeps
// against the scanned tree: a service resolves to the one .pc/.pcf file
// whose stem matches it (same one-match rule as the analyze selectors).
// Zero or several matches stay unresolved — never a silent pick. Depths
// stop at one level (no transitive roll-up): the target's own
// ComplexityScore is added to TpDepScore, and TpTotalScore carries the
// file-plus-dependencies cost the user asked to triage.
func resolveTpDeps(reports []*Report) {
	byStem := map[string][]*Report{}
	for _, r := range reports {
		byStem[svcStem(r.File)] = append(byStem[svcStem(r.File)], r)
	}
	for _, r := range reports {
		if len(r.TpSvcDeps) == 0 {
			continue
		}
		depScore := 0
		unresolved := 0
		var missing []string
		for i := range r.TpSvcDeps {
			d := &r.TpSvcDeps[i]
			if d.Service == tpDynamicService {
				unresolved++
				missing = append(missing, d.Service)
				continue
			}
			matches := byStem[strings.ToLower(d.Service)]
			if len(matches) != 1 {
				unresolved++
				missing = append(missing, d.Service)
				continue
			}
			t := matches[0]
			d.File = t.File
			d.Resolved = true
			d.Score = t.ComplexityScore
			d.Complexity = t.Complexity
			depScore += t.ComplexityScore
		}
		r.TpDepScore = depScore
		r.TpUnresolved = unresolved
		r.TpTotalScore = r.ComplexityScore + depScore
		r.Reasons += fmt.Sprintf("; tp svc dep score +%d", depScore)
		if unresolved > 0 {
			sort.Strings(missing)
			r.Reasons += fmt.Sprintf("; tp svc unresolved: %s", strings.Join(missing, ", "))
		}
	}
}
