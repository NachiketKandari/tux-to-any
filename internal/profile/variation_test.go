package profile

import "testing"

// gormalt is the synthetic second Go profile (P2 variation proof, AD14):
// a different project's conventions — gorm-shaped store, plural receiver
// policy — rendered over the same plan units gen/convert hold. If this
// profile cannot vary the conventions without touching the pipeline, the
// seam is wrong and no third target starts. (The Layout surface was
// deleted as dead API — engine-wiring audit Tier-2.)
type gormalt struct{}

func (gormalt) ID() string { return "gormalt" }

func (gormalt) Naming() Naming {
	return Naming{
		Request:  func(endpoint string) string { return endpoint + "Input" },
		Response: func(endpoint string) string { return endpoint + "Output" },
		Row:      func(methodName string) string { return methodName + "Result" },
		Receiver: func(service string) string { return lower(service) },
	}
}

func (gormalt) DB() DBRules {
	return DBRules{
		StoreReceiver: "r.repo.",
		TxVariants:    func(string) bool { return false }, // gorm wraps txs differently
	}
}

func TestVariationProofSecondGoProfile(t *testing.T) {
	var p Profile = gormalt{}
	if p.ID() != "gormalt" {
		t.Fatalf("id = %s", p.ID())
	}
	n := p.Naming()
	if got := n.Request("NavList"); got != "NavListInput" {
		t.Errorf("Request = %q", got)
	}
	if got := n.Row("GetNavDetails"); got != "GetNavDetailsResult" {
		t.Errorf("Row = %q", got)
	}
	db := p.DB()
	if db.StoreReceiver != "r.repo." {
		t.Errorf("StoreReceiver = %q", db.StoreReceiver)
	}
	if db.TxVariants("INSERT") {
		t.Errorf("gorm profile renders no tx variants")
	}
}

func lower(s string) string {
	b := []byte(s)
	if len(b) > 0 && b[0] >= 'A' && b[0] <= 'Z' {
		b[0] += 'a' - 'A'
	}
	return string(b)
}
