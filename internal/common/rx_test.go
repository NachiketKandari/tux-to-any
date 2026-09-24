package common

import "testing"

func TestCachedRegexpSamePointer(t *testing.T) {
	a := CachedRegexp(`\bfoo\b`)
	b := CachedRegexp(`\bfoo\b`)
	if a != b {
		t.Fatalf("same pattern should return the same instance")
	}
	if !a.MatchString("a foo b") {
		t.Fatalf("cached regexp should match")
	}
	if a.MatchString("a foobar b") {
		t.Fatalf("word boundary must hold through the cache")
	}
}

func TestCachedRegexpDistinctPatterns(t *testing.T) {
	a := CachedRegexp(`\bfoo\b`)
	b := CachedRegexp(`\bbar\b`)
	if a == b {
		t.Fatalf("distinct patterns must not share an instance")
	}
	if !b.MatchString("bar") || b.MatchString("foo") {
		t.Fatalf("distinct cached patterns must keep their own semantics")
	}
}
