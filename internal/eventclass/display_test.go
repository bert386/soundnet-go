package eventclass

import (
	"strings"
	"testing"
)

// observedOnTheStation is the ground truth for this file: scientific/common
// pairs read out of the running station's own API, not reconstructed from
// reading the splitter.
//
// They are pinned because reasoning about which splitter runs is exactly what
// went wrong. An earlier version of DisplayName keyed on two tokens, having
// read detection.ParseSpeciesString (SplitN into three) and assumed it described
// the read path. It does not: datastore.ResolveLabelNames cuts at the first
// underscore only. The test that version shipped with passed, because it drove
// the same wrong splitter the code did.
var observedOnTheStation = []struct{ scientific, common, want string }{
	// Multi-token: the common name is the whole remainder after the first "_".
	{"propeller", "and_airscrew", "Propeller, airscrew"},
	{"police", "car_(siren)", "Police car (siren)"},
	// No separator in the label: the resolver supplies the display form, so the
	// two differ only by case.
	{"vehicle", "Vehicle", "Vehicle"},
	{"thunder", "Thunder", "Thunder"},
	{"thunderstorm", "Thunderstorm", "Thunderstorm"},
	{"car", "Car", "Car"},
}

func TestDisplayNameMatchesWhatTheStationStores(t *testing.T) {
	for _, tc := range observedOnTheStation {
		got, ok := DisplayName(tc.scientific, tc.common)
		if !ok {
			t.Errorf("DisplayName(%q, %q) not found, want %q", tc.scientific, tc.common, tc.want)
			continue
		}
		if got != tc.want {
			t.Errorf("DisplayName(%q, %q) = %q, want %q", tc.scientific, tc.common, got, tc.want)
		}
	}
}

// TestDisplayNameRoundTripsTheReadPathSplit sweeps the whole taxonomy through the
// split the datastore actually performs.
//
// It mirrors datastore.ResolveLabelNames rather than importing it, because
// pulling the datastore package into this one costs a CGO sqlite build for a
// one-line function - but it is a single strings.Cut, and the pinned rows above
// are what proves the mirror is faithful. If those two ever disagree, the rows
// win: they came off the running station.
func TestDisplayNameRoundTripsTheReadPathSplit(t *testing.T) {
	for _, c := range allClasses() {
		raw := RawLabel(c.Label)
		sci, common, found := strings.Cut(raw, "_")
		if !found {
			// No separator: ResolveLabelNames leaves the common name empty and a
			// resolver fills it with the display form, which is what the station
			// shows. Both spellings must resolve.
			for _, common := range []string{"", sci, c.Label} {
				got, ok := DisplayName(sci, common)
				if !ok || got != c.Label {
					t.Errorf("DisplayName(%q, %q) = %q (ok=%v), want %q", sci, common, got, ok, c.Label)
				}
			}
			continue
		}
		got, ok := DisplayName(sci, common)
		if !ok || got != c.Label {
			t.Errorf("DisplayName(%q, %q) = %q (ok=%v), want %q", sci, common, got, ok, c.Label)
		}
	}
}

// TestDisplayNameAlsoAcceptsTheWritePathSplit covers the other splitter, which
// produces a three-part label. Kept as a fallback rather than removed: both
// exist in the codebase, and a pair from either must name the same class.
func TestDisplayNameAlsoAcceptsTheWritePathSplit(t *testing.T) {
	cases := []struct{ scientific, common, want string }{
		{"propeller", "and", "Propeller, airscrew"},
		{"fixed-wing", "aircraft", "Fixed-wing aircraft, airplane"},
		{"gunshot", "and", "Gunshot, gunfire"},
		{"aircraft", "engine", "Aircraft engine"},
		{"car", "passing", "Car passing by"},
		{"engine", "knocking", "Engine knocking"},
	}
	for _, tc := range cases {
		got, ok := DisplayName(tc.scientific, tc.common)
		if !ok || got != tc.want {
			t.Errorf("DisplayName(%q, %q) = %q (ok=%v), want %q",
				tc.scientific, tc.common, got, ok, tc.want)
		}
	}
}

// TestNamePrefixesAreUnique guards the assumption the last-resort lookup rests
// on. Without it a future class could share a prefix and the fallback would
// quietly return the wrong sibling's name.
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
		{"Rhipidura leucophrys", "Willie-wagtail"},
		{"Corvus coronoides", "Australian Raven"},
		{"", ""},
	}
	for _, s := range species {
		if got := DisplayNameFor(s.scientific, s.common); got != "" {
			t.Errorf("DisplayNameFor(%q, %q) = %q, want empty", s.scientific, s.common, got)
		}
	}
}
