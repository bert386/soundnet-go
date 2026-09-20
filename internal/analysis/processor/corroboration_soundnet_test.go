package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/bert386/soundnet-go/internal/conf"
)

// corroborationSettings returns settings with the mechanism configured, so the
// off-by-default tests and the behaviour tests cannot be confused for each other.
func corroborationSettings(threshold float64) *conf.Settings {
	s := &conf.Settings{}
	s.SoundNet.Enabled = true
	s.SoundNet.Enrichment.Enabled = true
	s.SoundNet.Enrichment.CorroborationThreshold = threshold
	// The ordinary threshold has to be real. A zero-valued BirdNET.Threshold
	// makes every detection clear the bar, so nothing is ever a candidate and
	// these tests pass by never exercising the thing they name.
	s.BirdNET.Threshold = 0.7
	return s
}

// TestCorroborationIsOffUnlessEveryOptInIsSet is the most important test here.
// This is the only part of detection that can make an outbound call about a
// detection the user would otherwise never have seen, so each gate is checked
// individually rather than trusting one combined condition.
func TestCorroborationIsOffUnlessEveryOptInIsSet(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		mutate func(*conf.Settings)
	}{
		{"soundnet disabled", func(s *conf.Settings) { s.SoundNet.Enabled = false }},
		{"enrichment disabled", func(s *conf.Settings) { s.SoundNet.Enrichment.Enabled = false }},
		{"threshold zero", func(s *conf.Settings) { s.SoundNet.Enrichment.CorroborationThreshold = 0 }},
		{"threshold negative", func(s *conf.Settings) { s.SoundNet.Enrichment.CorroborationThreshold = -1 }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := corroborationSettings(0.15)
			tc.mutate(s)

			_, on := soundNetCorroborationThreshold(s)
			assert.False(t, on, "corroboration must stay off")

			// And the threshold seam must hand back exactly what it was given.
			assert.InDelta(t, 0.7,
				soundNetCandidateThreshold(s, "propeller", "and_airscrew", 0.7), 1e-6)
		})
	}

	// Nil settings must not panic on a path that runs for every detection.
	_, on := soundNetCorroborationThreshold(nil)
	assert.False(t, on)
}

// TestCandidateThresholdOnlyMovesForEnrichableClasses pins the blast radius. A
// lowered bar on a class no authority can speak for would admit quiet noise and
// then have no way to reject it.
func TestCandidateThresholdOnlyMovesForEnrichableClasses(t *testing.T) {
	t.Parallel()
	s := corroborationSettings(0.15)

	cases := []struct {
		name               string
		scientific, common string
		want               float32
	}{
		// Aircraft: an authority exists, so candidates are admitted.
		{"aircraft class", "propeller", "and_airscrew", 0.15},
		{"jet engine", "jet", "engine", 0.15},
		// Vehicle and Engine are ambiguous with aircraft, so ADS-B may settle
		// them - the same reasoning as internal/eventclass/ambiguity.go.
		{"the vehicle superclass", "vehicle", "Vehicle", 0.15},
		// A road-specific class has no authority. Unchanged.
		{"a car is a car", "car", "Car", 0.7},
		{"a truck", "truck", "Truck", 0.7},
		// Birds, overwhelmingly the common case, must be untouched.
		{"a bird", "Trichoglossus moluccanus", "Rainbow Lorikeet", 0.7},
		{"another bird", "Acridotheres tristis", "Common Myna", 0.7},
		// An impulse has no authority either.
		{"a gunshot", "gunshot", "and_gunfire", 0.7},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := soundNetCandidateThreshold(s, tc.scientific, tc.common, 0.7)
			assert.InDelta(t, tc.want, got, 1e-6,
				"%s/%s", tc.scientific, tc.common)
		})
	}
}

// TestCandidateThresholdNeverRaisesTheBar guards the direction. A configured
// threshold above the model's own must not make a class harder to detect than
// it was - the setting exists to admit candidates, not to suppress detections.
func TestCandidateThresholdNeverRaisesTheBar(t *testing.T) {
	t.Parallel()
	s := corroborationSettings(0.9)
	got := soundNetCandidateThreshold(s, "propeller", "and_airscrew", 0.7)
	assert.InDelta(t, 0.7, got, 1e-6, "a higher candidate floor must be ignored, not applied")
}

// TestOnlyCandidatesAreSentForCorroboration checks the other half: a detection
// that already clears the ordinary threshold is an ordinary detection, and
// asking an authority about it would spend a credit to learn nothing that
// changes the outcome.
func TestOnlyCandidatesAreSentForCorroboration(t *testing.T) {
	t.Parallel()
	s := corroborationSettings(0.15)
	const normal = 0.7

	cases := []struct {
		name       string
		label      string
		confidence float32
		want       bool
	}{
		{"a quiet aircraft needs backing up", "propeller_and_airscrew", 0.20, true},
		{"the vehicle superclass, quiet", "vehicle", 0.30, true},
		{"a loud aircraft stands on its own", "propeller_and_airscrew", 0.85, false},
		{"exactly at the normal threshold", "propeller_and_airscrew", 0.70, false},
		{"below the candidate floor is still noise", "propeller_and_airscrew", 0.05, false},
		{"a quiet bird is not a candidate", "Acridotheres tristis_Common Myna", 0.20, false},
		{"a quiet car has nothing to ask", "car", 0.20, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := soundNetNeedsCorroboration(s, tc.label, tc.confidence, normal)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestUncorroboratedCandidatesAreDiscardedWhenNothingCanAnswer is the safety
// property stated directly: with no analyser installed nothing can corroborate,
// so every candidate must be dropped. The failure this guards against is the
// opposite default - admitting quiet detections because the check could not run.
func TestUncorroboratedCandidatesAreDiscardedWhenNothingCanAnswer(t *testing.T) {
	t.Parallel()

	p := &Processor{Settings: corroborationSettings(0.15), EventTracker: NewEventTracker(0)}
	item := &PendingDetection{Confidence: 0.20, BestModelID: "BirdNET_V2.4"}
	item.Detection.Result.RawLabel = "propeller_and_airscrew"

	nothingAnswers := func(string, time.Time, float32) bool { return false }
	discard, reason := p.soundNetDiscardWith(item, p.Settings, nothingAnswers)
	assert.True(t, discard, "a candidate nothing vouched for must not become a detection")
	assert.NotEmpty(t, reason, "the discard reason is shown to the operator")
}

// TestCorroboratedCandidatesSurvive is the other direction, and the whole point
// of the feature: a quiet aircraft an authority vouches for becomes a detection
// that would otherwise never have existed.
func TestCorroboratedCandidatesSurvive(t *testing.T) {
	t.Parallel()

	p := &Processor{Settings: corroborationSettings(0.15), EventTracker: NewEventTracker(0)}
	item := &PendingDetection{Confidence: 0.15, BestModelID: "BirdNET_V2.4"}
	item.Detection.Result.RawLabel = "propeller_and_airscrew"

	var askedFor string
	adsbAnswers := func(label string, _ time.Time, _ float32) bool {
		askedFor = label
		return true
	}
	discard, _ := p.soundNetDiscardWith(item, p.Settings, adsbAnswers)
	assert.False(t, discard, "0.15 beside a confirmed overflight is a detection")
	assert.Equal(t, "propeller_and_airscrew", askedFor,
		"the raw classifier label is what the taxonomy resolves against")
}

// TestOrdinaryDetectionsAreNeverDiscardedHere makes sure the new gate cannot
// swallow a detection it was never meant to see. A loud aircraft, and every
// bird at any confidence, must pass straight through.
func TestOrdinaryDetectionsAreNeverDiscardedHere(t *testing.T) {
	t.Parallel()

	p := &Processor{Settings: corroborationSettings(0.15), EventTracker: NewEventTracker(0)}

	cases := []struct {
		name       string
		label      string
		confidence float64
	}{
		{"a loud aircraft", "propeller_and_airscrew", 0.85},
		{"a quiet bird", "Acridotheres tristis_Common Myna", 0.20},
		{"a loud bird", "Acridotheres tristis_Common Myna", 0.95},
		{"a quiet car", "car", 0.20},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			item := &PendingDetection{Confidence: tc.confidence, BestModelID: "BirdNET_V2.4"}
			item.Detection.Result.RawLabel = tc.label
			var asked bool
			discard, _ := p.soundNetDiscardWith(item, p.Settings,
				func(string, time.Time, float32) bool { asked = true; return false })
			assert.False(t, discard)
			assert.False(t, asked, "no authority should have been asked about this at all")
		})
	}
}
