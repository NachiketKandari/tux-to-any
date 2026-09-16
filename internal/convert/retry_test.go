package convert

import (
	"strings"
	"testing"
)

// TestSeamMessagesModes pins the retry methodology switch: both modes send
// the same system + prompt + notes turns; repair mode inserts the prior
// gated payload as an assistant turn so the model patches it.
func TestSeamMessagesModes(t *testing.T) {
	const (
		sys    = "sys"
		prompt = "base prompt"
		prev   = "prior body"
		notes  = "line 3: declared and not used: cnt"
	)

	// No notes: nothing to repair, prev is inert in both modes.
	for _, repair := range []bool{false, true} {
		msgs := seamMessages(sys, prompt, prev, fixHintMethodBody, nil, repair)
		if len(msgs) != 2 || msgs[0].Role != "system" || msgs[1].Role != "user" {
			t.Fatalf("repair=%v: first attempt messages = %+v", repair, msgs)
		}
	}

	roll := seamMessages(sys, prompt, prev, fixHintMethodBody, []string{notes}, false)
	if len(roll) != 3 {
		t.Fatalf("roll retry must be system+prompt+notes, got %+v", roll)
	}
	if roll[2].Role != "user" || !strings.Contains(roll[2].Content, notes) || !strings.Contains(roll[2].Content, "Fix these errors") {
		t.Fatalf("roll feedback turn wrong: %+v", roll[2])
	}

	repair := seamMessages(sys, prompt, prev, fixHintMethodBody, []string{notes}, true)
	if len(repair) != 4 {
		t.Fatalf("repair retry must add the assistant turn, got %d messages", len(repair))
	}
	if repair[2].Role != "assistant" || repair[2].Content != prev {
		t.Fatalf("repair assistant turn = %+v, want the prior payload", repair[2])
	}
	if repair[3].Role != "user" || !strings.Contains(repair[3].Content, notes) {
		t.Fatalf("repair feedback turn wrong: %+v", repair[3])
	}

	// Repair with no usable prior (transport/over-cap failure): notes only.
	bare := seamMessages(sys, prompt, "  ", fixHintMethod, []string{notes}, true)
	if len(bare) != 3 || bare[2].Role != "user" {
		t.Fatalf("empty prev must fall back to roll shape, got %+v", bare)
	}
}
