package gen

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"

	"tux-to-any/internal/telemetry"
)

// MockTarget names one interface to regenerate with mockgen.
type MockTarget struct {
	Source string // interface file path
	Dest   string // mock output path
	Name   string // interface name
}

// MockTargetsFor derives the standard db/controller mock targets (A4.3) —
// the one table for the artifact conventions (mock_store.go /
// mock_controller.go, `<Service>Store` / `<Service>Controller`). Paths that
// do not exist are omitted (gentest best-effort semantics); convert passes
// existence through to RunMocks, so the filter is harmless there.
func MockTargetsFor(serviceDir, service string) []MockTarget {
	return []MockTarget{
		{
			Source: filepath.Join(serviceDir, "db", "interface.go"),
			Dest:   filepath.Join(serviceDir, "db", "mock_store.go"),
			Name:   service + "Store",
		},
		{
			Source: filepath.Join(serviceDir, "controller", "interface.go"),
			Dest:   filepath.Join(serviceDir, "controller", "mock_controller.go"),
			Name:   service + "Controller",
		},
	}
}

// mockgenVersion pins the uber-go/mock release the go-run fallback uses
// when no mockgen binary is on PATH (verified against v0.6.0). Pinned,
// never floating: the generated mocks must match the gomock runtime the
// target module vendors, and a floating tag would move under us.
const mockgenVersion = "v0.6.0"

// RunMocks regenerates the uber-go/mock doubles for targets. The mockgen
// binary wins when present (fast, offline); otherwise the run falls back to
// `go run go.uber.org/mock/mockgen@<pinned>` so generation never depends on
// machine setup — it is one command either way. Missing sources are skipped
// silently; any other failure is best-effort (WARN, never fatal). Shared by
// `convert` and `gentest` (PRD-2026-09-09 GT-D7).
func RunMocks(ctx context.Context, targets []MockTarget) {
	log := telemetry.Log(ctx)
	var live []MockTarget
	for _, t := range targets {
		if _, err := os.Stat(t.Source); err != nil {
			continue
		}
		live = append(live, t)
	}
	if len(live) == 0 {
		return
	}
	binary, err := exec.LookPath("mockgen")
	useGoRun := err != nil
	if useGoRun {
		log.Info("mockgen not on PATH — generating via go run", "module", "go.uber.org/mock/mockgen@"+mockgenVersion)
	}
	for _, t := range live {
		if out, err := mockgenCmd(binary, useGoRun, t).CombinedOutput(); err != nil {
			log.Warn("mockgen failed (best-effort)", "interface", t.Name, "output", string(out))
		} else {
			log.Info("mocks generated", "interface", t.Name, "path", t.Dest)
		}
	}
}

// mockgenCmd builds the mock regeneration command for one target: the local
// binary when available, else the pinned go-run fallback with identical
// generation flags.
func mockgenCmd(binary string, useGoRun bool, t MockTarget) *exec.Cmd {
	args := []string{"-source", t.Source, "-destination", t.Dest, "-package", filepath.Base(filepath.Dir(t.Dest)), t.Name}
	if useGoRun {
		return exec.Command("go", append([]string{"run", "go.uber.org/mock/mockgen@" + mockgenVersion}, args...)...)
	}
	return exec.Command(binary, args...)
}
