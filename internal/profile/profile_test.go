package profile

import "testing"

func TestDefaultIsGonav(t *testing.T) {
	if p := Default(); p.ID() != "gonav" {
		t.Fatalf("default profile = %s, want gonav", p.ID())
	}
}

// TestGonavPinsTodayConventions pins profile #1 to the conventions P1 will
// extract from gen/convert — the byte-identity contract's reference values.
func TestGonavPinsTodayConventions(t *testing.T) {
	n := Default().Naming()
	if got := n.Request("NavList"); got != "NavListRequest" {
		t.Errorf("Request = %q", got)
	}
	if got := n.Response("NavList"); got != "NavListResponse" {
		t.Errorf("Response = %q", got)
	}
	for method, want := range map[string]string{
		"GetNavDetails":     "NavDetails",
		"MergeDemoAccounts": "DemoAccounts", // A2.6's verb-strip contract
		"InsertDate":        "Date",
	} {
		if got := n.Row(method); got != want {
			t.Errorf("Row(%q) = %q, want %q", method, got, want)
		}
	}
	if got := n.Receiver("Nav"); got != "nav" {
		t.Errorf("Receiver = %q", got)
	}
	db := Default().DB()
	if db.StoreReceiver != "s.store." {
		t.Errorf("StoreReceiver = %q", db.StoreReceiver)
	}
	for qt, want := range map[string]bool{"INSERT": true, "UPDATE": true, "DELETE": true, "MERGE": false, "SELECT_SINGLE": false} {
		if got := db.TxVariants(qt); got != want {
			t.Errorf("TxVariants(%q) = %v", qt, got)
		}
	}
}
