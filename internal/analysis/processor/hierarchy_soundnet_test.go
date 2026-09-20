package processor

import (
	"testing"

	"github.com/bert386/soundnet-go/internal/classifier"
	"github.com/bert386/soundnet-go/internal/datastore"
	"github.com/bert386/soundnet-go/internal/eventclass"
)

// admitAbove models the station's real admission test: a result is stored when
// it clears the threshold in force for it.
func admitAbove(threshold float32) func(datastore.Results) bool {
	return func(r datastore.Results) bool { return r.Confidence > threshold }
}

func labels(results []datastore.Results) []string {
	out := make([]string, len(results))
	for i := range results {
		out[i] = results[i].Species
	}
	return out
}

// The case the operator asked about: an overflight scores highest on Vehicle,
// AudioSet's parent class, and is recorded as road traffic beside the aircraft
// row that names it correctly.
func TestPreferSpecificDropsTheParentWhenTheChildWillBeStored(t *testing.T) {
	t.Parallel()

	results := []datastore.Results{
		{Species: "vehicle", Confidence: 0.54},
		{Species: "aircraft", Confidence: 0.41},
		{Species: "bird", Confidence: 0.30},
	}

	got := soundNetPreferSpecificWith(results, classifier.RegistryIDCED, admitAbove(0.15))

	want := []string{"aircraft", "bird"}
	if diff := labels(got); len(diff) != len(want) || diff[0] != want[0] || diff[1] != want[1] {
		t.Fatalf("got %v, want %v", diff, want)
	}
}

// The condition that makes the rule safe. A child below its own threshold is
// not going to be stored, so dropping the parent would turn a sound the station
// detected into one it did not.
func TestPreferSpecificKeepsTheParentWhenTheChildWouldNotBeStored(t *testing.T) {
	t.Parallel()

	results := []datastore.Results{
		{Species: "vehicle", Confidence: 0.24},
		{Species: "aircraft", Confidence: 0.14},
	}

	got := soundNetPreferSpecificWith(results, classifier.RegistryIDCED, admitAbove(0.15))

	if len(got) != 2 {
		t.Fatalf("got %v, want both kept: dropping the parent here loses the detection entirely", labels(got))
	}
}

// A weak aircraft score beside a strong Vehicle is likelier a road vehicle that
// leaked a little into the aircraft classes. On the operator's clips no
// aircraft window ever fell below 0.78 of its parent.
func TestPreferSpecificKeepsTheParentWhenTheChildIsRelativelyWeak(t *testing.T) {
	t.Parallel()

	results := []datastore.Results{
		{Species: "vehicle", Confidence: 0.90},
		{Species: "aircraft", Confidence: 0.20}, // above threshold, but 0.22 of the parent
	}

	got := soundNetPreferSpecificWith(results, classifier.RegistryIDCED, admitAbove(0.15))

	if len(got) != 2 {
		t.Fatalf("got %v, want both kept", labels(got))
	}
}

// A sibling from the parent's own domain is the same reading, not a more
// specific one: Car does not make Vehicle wrong.
func TestPreferSpecificIgnoresASiblingFromTheSameDomain(t *testing.T) {
	t.Parallel()

	results := []datastore.Results{
		{Species: "vehicle", Confidence: 0.50},
		{Species: "car", Confidence: 0.45},
	}

	got := soundNetPreferSpecificWith(results, classifier.RegistryIDCED, admitAbove(0.15))

	if len(got) != 2 {
		t.Fatalf("got %v, want both kept", labels(got))
	}
}

// A class that determines its own domain has nothing to be corrected to.
func TestPreferSpecificLeavesAnUnambiguousClassAlone(t *testing.T) {
	t.Parallel()

	results := []datastore.Results{
		{Species: "helicopter", Confidence: 0.60},
		{Species: "aircraft", Confidence: 0.55},
	}

	got := soundNetPreferSpecificWith(results, classifier.RegistryIDCED, admitAbove(0.15))

	if len(got) != 2 {
		t.Fatalf("got %v, want both kept", labels(got))
	}
}

// Upstream decides what BirdNET's own labels mean. Reshaping them would be a
// different change from this one, and would reach every BirdNET-Go user.
func TestPreferSpecificDoesNotTouchBirdModels(t *testing.T) {
	t.Parallel()

	results := []datastore.Results{
		{Species: "vehicle", Confidence: 0.54},
		{Species: "aircraft", Confidence: 0.41},
	}

	got := soundNetPreferSpecificWith(results, "BirdNET_V2.4", admitAbove(0.15))

	if len(got) != 2 {
		t.Fatalf("got %v, want the results returned untouched", labels(got))
	}
}

// Thunder and Thunderstorm are in the ambiguity table for the same reason:
// nineteen of nineteen reviewed at this station were passing jets.
func TestPreferSpecificCorrectsThunder(t *testing.T) {
	t.Parallel()

	results := []datastore.Results{
		{Species: "thunderstorm", Confidence: 0.89},
		{Species: "jet_engine", Confidence: 0.62},
	}

	got := soundNetPreferSpecificWith(results, classifier.RegistryIDYAMNet, admitAbove(0.15))

	if names := labels(got); len(names) != 1 || names[0] != "jet_engine" {
		t.Fatalf("got %v, want only jet_engine", names)
	}
}

// Nothing to do is the common case and must not allocate a new slice.
func TestPreferSpecificReturnsTheSameSliceWhenNothingChanges(t *testing.T) {
	t.Parallel()

	results := []datastore.Results{
		{Species: "bird", Confidence: 0.90},
		{Species: "insect", Confidence: 0.40},
	}

	got := soundNetPreferSpecificWith(results, classifier.RegistryIDCED, admitAbove(0.15))

	if &got[0] != &results[0] {
		t.Fatal("a chunk with nothing to correct was copied")
	}
}

// Two of the tests above assert that nothing is dropped, which is also what
// happens when a label fails to resolve at all - so they would pass for the
// wrong reason if these labels were not in the taxonomy. Pinning the premise.
func TestPreferSpecificTestLabelsResolveAsAssumed(t *testing.T) {
	t.Parallel()

	for label, want := range map[string]eventclass.Domain{
		"vehicle":      eventclass.DomainVehicle,
		"car":          eventclass.DomainVehicle,
		"aircraft":     eventclass.DomainAircraft,
		"helicopter":   eventclass.DomainAircraft,
		"jet_engine":   eventclass.DomainAircraft,
		"thunderstorm": eventclass.DomainWeather,
	} {
		class, found := eventclass.Resolve(label)
		if !found {
			t.Errorf("%q does not resolve; a test relying on it proves nothing", label)
			continue
		}
		if class.Domain != want {
			t.Errorf("%q is in domain %q, the test assumes %q", label, class.Domain, want)
		}
	}

	if !eventclass.IsAmbiguous("Vehicle") || !eventclass.IsAmbiguous("Thunderstorm") {
		t.Error("Vehicle and Thunderstorm must be ambiguous or the rule never fires")
	}
	if eventclass.IsAmbiguous("Helicopter") {
		t.Error("Helicopter determines its own domain; the unambiguous test assumes so")
	}
}

// The ratio sits between the two things that were actually measured, and it is
// worth failing loudly if either anchor is crossed by a later tweak.
//
//	0.08 - the truck clip, best aircraft class 0.055 against Vehicle 0.693
//	0.35 - the lowest window of a jet confirmed overhead by ADS-B
func TestPreferSpecificRatioSitsBetweenTheMeasuredAnchors(t *testing.T) {
	t.Parallel()

	const (
		loudestFalseAircraftRatio = 0.08
		faintestRealAircraftRatio = 0.35
	)

	if soundNetSpecificRatio <= loudestFalseAircraftRatio {
		t.Fatalf("ratio %v does not clear the truck at %v; a road vehicle would be relabelled an aircraft",
			soundNetSpecificRatio, loudestFalseAircraftRatio)
	}
	if soundNetSpecificRatio >= faintestRealAircraftRatio {
		t.Fatalf("ratio %v is above the faintest confirmed aircraft at %v; real overflights would be refused",
			soundNetSpecificRatio, faintestRealAircraftRatio)
	}
}

// The window that made the first shipped ratio wrong: a confirmed jet whose
// aircraft class reached 0.35 of Vehicle. At 0.5 this was refused.
func TestPreferSpecificCorrectsAConfirmedJetAtTheLowEnd(t *testing.T) {
	t.Parallel()

	results := []datastore.Results{
		{Species: "vehicle", Confidence: 0.432},
		{Species: "aircraft", Confidence: 0.151},
	}

	got := soundNetPreferSpecificWith(results, classifier.RegistryIDCED, admitAbove(0.15))

	if names := labels(got); len(names) != 1 || names[0] != "aircraft" {
		t.Fatalf("got %v, want only aircraft", names)
	}
}
