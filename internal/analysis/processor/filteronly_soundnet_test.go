package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bert386/soundnet-go/internal/classifier"
)

// TestFilterOnlyClassesAreNotStored covers the exact rows the operator reviewed.
//
// Each of these was stored as a detection with no domain, no display name and a
// mangled label, at a confidence too low to trip the privacy filter that was the
// only reason it was emitted.
func TestFilterOnlyClassesAreNotStored(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		rawLabel string
		drop     bool
		why      string
	}{
		// Reviewed by the operator as "rustling dry grass", four times over.
		{"chewing", "chewing_and_mastication", true, "a human class that is not an event"},
		// Reviewed as "human cough".
		{"burping", "burping_and_eructation", true, "a human class that is not an event"},
		// Reviewed as "a crow".
		{"crying", "crying_and_sobbing", true, "a human class that is not an event"},
		// The class the privacy filter actually exists for. Emitted so the filter
		// can see it; never a detection.
		{"speech", "speech", true, "the privacy filter's whole purpose"},
		{"bark", "bark", true, "a dog-filter input that is not the taxonomy's Dog"},

		// Dog is both a filter input and a default-enabled biological event.
		// Taxonomy membership wins: a barking dog is worth recording.
		{"dog", "dog", false, "a real event the taxonomy carries"},

		// Ordinary event classes are untouched - they are not filter inputs.
		{"aircraft", "propeller_and_airscrew", false, "an event class"},
		{"thunder", "thunder", false, "an event class"},
		{"vehicle", "vehicle", false, "an event class"},
		// And every bird.
		{"a bird", "Acridotheres tristis_Common Myna", false, "a species"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := soundNetFilterOnlyClass(classifier.RegistryIDYAMNet, tc.rawLabel)
			assert.Equal(t, tc.drop, got, "%s: %s", tc.rawLabel, tc.why)
		})
	}
}

// TestOtherModelsAreUntouched pins the blast radius. BirdNET has its own
// non-species labels and upstream decides what happens to them; quietly changing
// that for every BirdNET-Go user would be a different change than this one.
func TestOtherModelsAreUntouched(t *testing.T) {
	t.Parallel()

	for _, modelID := range []string{
		classifier.RegistryIDBirdNETV24,
		classifier.RegistryIDPerchV2,
		classifier.RegistryIDBat,
		"",
	} {
		// Even a label that would certainly be dropped for YAMNet.
		assert.False(t, soundNetFilterOnlyClass(modelID, "speech"),
			"model %q must be left alone", modelID)
	}
}
