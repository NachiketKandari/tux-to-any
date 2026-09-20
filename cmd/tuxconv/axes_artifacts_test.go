package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tux-to-any/internal/flow"
)

// TestAxesArtifactsWrite pins the registry twin's write contract: both
// files land in the given dir, the JSON parses back to the report, and the
// markdown carries one row per axis (scenario-filter plan §3).
func TestAxesArtifactsWrite(t *testing.T) {
	dir := t.TempDir()
	axes := []*flow.DispatchAxis{
		{
			Ref: "c_flag", RefName: "c_flag", Domain: []string{"F", "H", "I"}, Sites: 3,
			Kind: flow.AxisPrimary, GuardLines: []int{4, 6, 8}, HasDefault: true,
		},
		{
			Ref: "new_flag", RefName: "new_flag", Domain: []string{"J", "K"}, Sites: 2,
			Kind: flow.AxisSecondary, GuardLines: []int{12, 14}, HasDefault: true,
		},
	}
	paths, err := axesArtifacts(nil, dir, "demo.pc", axes)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Fatalf("paths = %v, want the json + md twins", paths)
	}
	data, err := os.ReadFile(filepath.Join(dir, "demo.pc.axes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rep flow.AxesReport
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("json twin unparseable: %v", err)
	}
	if rep.Entry != "demo.pc" || len(rep.Axes) != 2 {
		t.Fatalf("report = %q/%d axes, want demo.pc/2", rep.Entry, len(rep.Axes))
	}
	if rep.Axes[1].Kind != flow.AxisSecondary || len(rep.Axes[1].GuardLines) != 2 {
		t.Errorf("secondary lost in json: %+v", rep.Axes[1])
	}
	md, err := os.ReadFile(filepath.Join(dir, "demo.pc.axes.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# Dispatch axes — demo.pc",
		"| 0 | c_flag | c_flag | — | primary | F, H, I | 3 | 4, 6, 8 | yes (default) |",
		"| 1 | new_flag | new_flag | — | secondary | J, K | 2 | 12, 14 | yes (default) |",
	} {
		if !strings.Contains(string(md), want) {
			t.Errorf("axes md missing %q:\n%s", want, md)
		}
	}
}
