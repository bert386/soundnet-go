package eventclass

import (
	"slices"
	"testing"
)

func TestCandidateDomainsPutTheClassOwnDomainFirst(t *testing.T) {
	for _, c := range allClasses() {
		got := c.CandidateDomains()
		if len(got) == 0 {
			t.Errorf("%q has no candidate domains at all", c.Label)
			continue
		}
		if got[0] != c.Domain {
			t.Errorf("%q: first candidate is %q, want its own domain %q",
				c.Label, got[0], c.Domain)
		}
		if i := slices.Index(got[1:], c.Domain); i >= 0 {
			t.Errorf("%q lists its own domain twice", c.Label)
		}
	}
}

// TestAmbiguityIsRareAndDeliberate keeps the table from growing by accident. Its
// cost is an API credit per detection of every class in it, so a class earns a
// place here by being a genuine AudioSet superclass, not by being confusable.
// Confusability is what ConfusionSet is for.
func TestAmbiguityIsRareAndDeliberate(t *testing.T) {
	want := map[string]bool{"Vehicle": true, "Engine": true}
	for _, c := range allClasses() {
		ambiguous := len(c.CandidateDomains()) > 1
		if ambiguous != want[c.Label] {
			t.Errorf("%q: ambiguous = %v, want %v", c.Label, ambiguous, want[c.Label])
		}
	}
}

// TestRoadSpecificClassesStayUnenrichable is the rule this feature must not
// erode: road traffic has no public authority, so a class that really is a car
// must still resolve to no identity rather than a guessed one.
func TestRoadSpecificClassesStayUnenrichable(t *testing.T) {
	for _, label := range []string{"Car", "Car passing by", "Truck", "Motorcycle", "Traffic noise, roadway noise"} {
		c, ok := Lookup(label)
		if !ok {
			t.Fatalf("%q is not in the taxonomy; this test is out of date", label)
		}
		if c.Enrichable() {
			t.Errorf("%q is enrichable; no authority can identify a passing car", label)
		}
	}
}

func TestSuperclassesAreEnrichable(t *testing.T) {
	for _, label := range []string{"Vehicle", "Engine"} {
		c, ok := Lookup(label)
		if !ok {
			t.Fatalf("%q is not in the taxonomy", label)
		}
		if !c.Enrichable() {
			t.Errorf("%q is not enrichable, so an overflight recorded under it never reaches ADS-B", label)
		}
		if c.Domain.Enrichable() {
			t.Errorf("Domain(%q).Enrichable() is true; the domain itself must stay honest - "+
				"it is the label that is ambiguous, not the domain", c.Domain)
		}
	}
}

// TestIsAmbiguousAcceptsEveryLabelForm guards the trap that has caused two silent
// bugs on this project: a label reaches this package as a display name, a raw
// label or the truncated name the datastore stores, and a check that accepts
// only one of them fails exactly where it matters.
func TestIsAmbiguousAcceptsEveryLabelForm(t *testing.T) {
	for _, label := range []string{"Vehicle", "vehicle", "Engine", "engine"} {
		if !IsAmbiguous(label) {
			t.Errorf("IsAmbiguous(%q) = false, want true", label)
		}
	}
	for _, label := range []string{"Car", "car", "Helicopter", "helicopter", "Thunder"} {
		if IsAmbiguous(label) {
			t.Errorf("IsAmbiguous(%q) = true, want false", label)
		}
	}
}
