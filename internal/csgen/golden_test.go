package csgen

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"tux-to-any/internal/cschk"
	"tux-to-any/internal/csplan"
	"tux-to-any/internal/ir"
)

// The convertcs golden: the full deterministic pipeline (IR → csplan →
// csgen → cschk gates) over the synthetic CUSE-shaped fixture, byte-pinned
// under testdata/goldens/cs/<component>/. Regenerate deliberately with
// CS_UPDATE_GOLDENS=1 (only after a plan or template change).
const csUpdateGoldens = "CS_UPDATE_GOLDENS"

func TestConvertcsGolden(t *testing.T) {
	const (
		fixture    = "../../testdata/fixtures/cs/SVC_CUST_GET_DTL.pc"
		mappingPth = "../../testdata/fixtures/cs/cust.mapping.yaml"
		goldenDir  = "../../testdata/goldens/cs"
	)
	m, err := csplan.LoadMapping(mappingPth)
	if err != nil {
		t.Fatal(err)
	}
	irf, err := ir.ExtractFileOpts(fixture, ir.Options{})
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	p, err := csplan.Build(csplan.Options{Main: irf, Source: string(src), Mapping: m})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Endpoints) != 1 || p.Endpoints[0].Name != "CustomEvent" || len(p.Queries) != 1 {
		t.Fatalf("plan shape = %d endpoint(s), %d query(ies) — fixture drifted", len(p.Endpoints), len(p.Queries))
	}
	res, err := Generate(context.Background(), Options{Plan: p, NoLLM: true})
	if err != nil {
		t.Fatal(err)
	}

	// Gates must be clean on the pinned shape.
	var issues []cschk.Issue
	typeNames := map[string]string{
		"Controller/" + p.Controller + ".cs":   p.Controller,
		"DTO/" + p.DTOCls + ".cs":              p.DTOCls,
		"NamedQueries/" + p.QueriesCls + ".cs": p.QueriesCls,
		"Repository/I" + p.Repo + ".cs":        "I" + p.Repo,
		"Repository/" + p.Repo + ".cs":         p.Repo,
		"Service/I" + p.Service + ".cs":        "I" + p.Service,
		"Service/" + p.Service + ".cs":         p.Service,
	}
	for _, rel := range res.Order {
		issues = append(issues, cschk.Check(rel, res.Files[rel], typeNames[rel])...)
	}
	issues = append(issues, cschk.SQLFidelity(p, res.Files)...)
	issues = append(issues, cschk.OracleParams(p, res.Files)...)
	for _, is := range issues {
		t.Errorf("gate: %s", is.Error())
	}

	update := os.Getenv(csUpdateGoldens) == "1"
	for _, rel := range res.Order {
		golden := filepath.Join(goldenDir, p.Component, rel)
		if update {
			if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(golden, []byte(res.Files[rel]), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("golden missing (run with %s=1 to pin): %v", csUpdateGoldens, err)
		}
		if !bytes.Equal(want, []byte(res.Files[rel])) {
			t.Errorf("golden drift: %s differs from the pinned output", rel)
		}
	}
}
