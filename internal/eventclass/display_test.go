package eventclass

import (
	"testing"

	"github.com/bert386/soundnet-go/internal/detection"
)

// TestDisplayNameRoundTripsTheDatastoreSplit is the test this whole file exists
// for. It drives every taxonomy class through the *real* upstream parser rather
// than a local imitation of it, because the thing that keeps going wrong is an
// assumption about how a label is split, not the splitting itself.
//
// The first version of DisplayName rejoined the scientific and common names with
// an underscore and looked the result up. That is correct only for a two-token
// label: ParseSpeciesString splits into at most three parts, so
// "propeller_and_airscrew" arrives as ("propeller", "and", "airscrew") and the
// rejoined "propeller_and" matched nothing. Every single-word class passed,
// which is exactly the asymmetry that hid the same mistake twice before.
func TestDisplayNameRoundTripsTheDatastoreSplit(t *testing.T) {
	for _, c := range allClasses() {
		sp := detection.ParseSpeciesString(RawLabel(c.Label))
		got, ok := DisplayName(sp.ScientificName, sp.CommonName)
		if !ok {
			t.Errorf("DisplayName(%q, %q) not found; class %q stored as %+v",
				sp.ScientificName, sp.CommonName, c.Label, sp)
			continue
		}
		if got != c.Label {
			t.Errorf("DisplayName(%q, %q) = %q, want %q",
				sp.ScientificName, sp.CommonName, got, c.Label)
		}
	}
}

// TestNamePrefixesAreUnique guards the assumption DisplayName rests on: that two
// tokens identify a class. If a future class collides, the round-trip test above
// would still pass for one of the pair and quietly return the wrong name for the
// other, so the collision is asserted directly.
func TestNamePrefixesAreUnique(t *testing.T) {
	seen := make(map[string]string, len(byPrefixIndex))
	for _, c := range allClasses() {
		key := namePrefix(RawLabel(c.Label))
		if other, dup := seen[key]; dup {
			t.Errorf("prefix %q is shared by %q and %q; DisplayName cannot tell them apart",
				key, other, c.Label)
			continue
		}
		seen[key] = c.Label
	}
}

// TestDisplayNameLeavesSpeciesAlone checks the other half of the contract. The
// field is emitted into every detection response, so anything that is not an
// event class must produce nothing at all rather than a near miss.
func TestDisplayNameLeavesSpeciesAlone(t *testing.T) {
	species := []struct{ scientific, common string }{
		{"Trichoglossus moluccanus", "Rainbow Lorikeet"},
		{"Acridotheres tristis", "Common Myna"},
		{"Anthochaera chrysoptera", "Little Wattlebird"},
		// A genus whose first token is also an event class's first token would be
		// the dangerous case; none exists today, but the shape is worth pinning.
		{"Corvus", "Raven"},
		{"", ""},
	}
	for _, s := range species {
		if got := DisplayNameFor(s.scientific, s.common); got != "" {
			t.Errorf("DisplayNameFor(%q, %q) = %q, want empty", s.scientific, s.common, got)
		}
	}
}

// TestDisplayNameAmbiguousTruncations covers the three classes whose stored
// scientific name alone is ambiguous. These are the cases where the second token
// is doing the work, so they are named rather than left to the sweep above.
func TestDisplayNameAmbiguousTruncations(t *testing.T) {
	cases := []struct{ scientific, common, want string }{
		{"aircraft", "aircraft", "Aircraft"},
		{"aircraft", "engine", "Aircraft engine"},
		{"car", "car", "Car"},
		{"car", "passing", "Car passing by"},
		{"engine", "engine", "Engine"},
		{"engine", "knocking", "Engine knocking"},
		{"propeller", "and", "Propeller, airscrew"},
	}
	for _, tc := range cases {
		got, ok := DisplayName(tc.scientific, tc.common)
		if !ok || got != tc.want {
			t.Errorf("DisplayName(%q, %q) = %q (ok=%v), want %q",
				tc.scientific, tc.common, got, ok, tc.want)
		}
	}
}
