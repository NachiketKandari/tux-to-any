package convert

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tux-to-any/internal/budget"
)

const detSkeletonSrc = `package controller

func (s *navController) NavHistory(c context.Context) {
// tuxgo:deterministic-controller NavHistory — no-llm best-effort body
	getCount, err := s.store.GetCount(c)
	if err != nil {
		return nil, err
	}
	_ = getCount
	return nil, nil
}

// tuxgo:REJECTED-BEGIN SipFreedem
// func (s *navController) SipFreedem(
// tuxgo:REJECTED-END SipFreedem
`

// TestDetMethodSource pins the upgrade-baseline extraction: the live
// method's full text comes out, commented rejected placeholders never
// match, and unknown names yield "" (prompt unchanged).
func TestDetMethodSource(t *testing.T) {
	got := detMethodSource(detSkeletonSrc, "NavHistory")
	for _, want := range []string{
		"func (s *navController) NavHistory(c context.Context) {",
		"s.store.GetCount(c)",
		"_ = getCount",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("extraction missing %q:\n%s", want, got)
		}
	}
	if strings.HasSuffix(got, "\n") || !strings.HasSuffix(got, "}") {
		t.Errorf("extraction must end at the method's closing brace, got:\n%q", got)
	}
	if got := detMethodSource(detSkeletonSrc, "SipFreedem"); got != "" {
		t.Errorf("commented rejected placeholder extracted:\n%s", got)
	}
	if got := detMethodSource(detSkeletonSrc, "NoSuchMethod"); got != "" {
		t.Errorf("foreign method extracted:\n%s", got)
	}
}

// TestDetSkeletonSection pins the prompt section: empty input renders
// nothing; a method renders its bare statements fenced (no func wrapper —
// the seam OUTPUT rule demands bare statements and the gates reject the
// wrapped shape) plus the edit contract with the legal TODO-residue idiom.
func TestDetSkeletonSection(t *testing.T) {
	if got := detSkeletonSection("  \n"); got != "" {
		t.Errorf("empty method must render no section, got %q", got)
	}
	method := detMethodSource(detSkeletonSrc, "NavHistory")
	got := detSkeletonSection(method)
	for _, want := range []string{
		"Deterministic baseline",
		"```go",
		"s.store.GetCount(c)",
		"no func wrapper",
		"_ = name // tuxgo:TODO <role>",
		"never dropped, never left unused",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("section missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "func (s *navController)") {
		t.Errorf("section must not carry the func wrapper:\n%s", got)
	}
}

// TestDetSkeletonForBudget pins the 16k-window guard: the baseline rides
// when prompt+section fits MaxPromptTokens and is omitted (prompt
// byte-identical) when it would overflow; files without a deterministic
// method never alter the prompt.
func TestDetSkeletonForBudget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nav.go")
	if err := os.WriteFile(path, []byte(detSkeletonSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	prompt := "base prompt "

	roomy := Options{Budget: budget.New(10000, 4096, 4)}
	got := detSkeletonFor(ctx, roomy, prompt, path, "NavHistory", detControllerLanded)
	if !strings.HasPrefix(got, prompt) || !strings.Contains(got, "Deterministic baseline") {
		t.Errorf("roomy budget must attach the baseline:\n%s", got)
	}

	tight := Options{Budget: budget.New(budget.New(10000, 4096, 4).Count(prompt)+1, 4096, 4)}
	if got := detSkeletonFor(ctx, tight, prompt, path, "NavHistory", detControllerLanded); got != prompt {
		t.Errorf("over-budget baseline must leave the prompt identical, got:\n%s", got)
	}

	// No deterministic method on disk (rejected placeholder only): prompt
	// untouched even with room.
	if got := detSkeletonFor(ctx, roomy, prompt, path, "SipFreedem", detControllerLanded); got != prompt {
		t.Errorf("non-deterministic unit must leave the prompt identical, got:\n%s", got)
	}
	if got := detSkeletonFor(ctx, roomy, prompt, filepath.Join(dir, "missing.go"), "NavHistory", detControllerLanded); got != prompt {
		t.Errorf("missing file must leave the prompt identical, got:\n%s", got)
	}
}
