package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/bert386/soundnet-go/internal/conf"
	"github.com/bert386/soundnet-go/internal/detection"
)

// secondOpinionSettings is corroboration switched on, with YAMNet marked as a
// model whose non-bird labels need a second opinion - the station's config.
func secondOpinionSettings() *conf.Settings {
	s := corroborationSettings(0.15)
	s.SoundNet.Enrichment.RequireSecondOpinion = []string{"YAMNet"}
	return s
}

// pendingWith builds a pending detection with a separate best score per model.
func pendingWith(label string, scores map[string]float64, best string) *PendingDetection {
	item := &PendingDetection{
		BestModelID:        best,
		Confidence:         scores[best],
		FirstDetected:      time.Date(2026, 9, 21, 11, 0, 0, 0, time.UTC),
		ModelContributions: make(map[string]ModelContribution, len(scores)),
	}
	item.Detection.Result = detection.Result{RawLabel: label}
	for model, score := range scores {
		item.ModelContributions[model] = ModelContribution{HitCount: 1, MaxConfidence: score}
	}
	return item
}

// The wind case exactly as measured: YAMNet says Thunderstorm 0.92, CED says
// 0.2 - admitted only as a corroboration candidate. That is not agreement, and
// counting it as such let the wind straight through the first version.
func TestSecondOpinionIgnoresCandidateLevelAgreement(t *testing.T) {
	t.Parallel()
	s := secondOpinionSettings()

	weak := pendingWith("thunderstorm", map[string]float64{"YAMNet": 0.92, "CED": 0.2}, "YAMNet")
	assert.True(t, soundNetNeedsSecondOpinion(s, weak, "thunderstorm"),
		"CED at 0.2 would never have recorded this on its own")

	strong := pendingWith("thunderstorm", map[string]float64{"YAMNet": 0.92, "CED": 0.8}, "YAMNet")
	assert.False(t, soundNetNeedsSecondOpinion(s, strong, "thunderstorm"),
		"CED at 0.8 is a real second opinion")
}

// pending builds a pending detection heard by the given models.
func pending(label string, confidence float64, models ...string) *PendingDetection {
	item := &PendingDetection{
		Confidence:         confidence,
		FirstDetected:      time.Date(2026, 9, 21, 11, 0, 0, 0, time.UTC),
		ModelContributions: make(map[string]ModelContribution, len(models)),
	}
	item.Detection.Result = detection.Result{RawLabel: label}
	for i, model := range models {
		if i == 0 {
			item.BestModelID = model
		}
		item.ModelContributions[model] = ModelContribution{HitCount: 1, MaxConfidence: confidence}
	}
	return item
}

// The cases the rule exists for, taken from the operator's reviews.
func TestSecondOpinionDiscardsWhatOnlyYAMNetHeard(t *testing.T) {
	t.Parallel()
	p := &Processor{}
	s := secondOpinionSettings()

	never := func(string, time.Time, float32) bool {
		t.Fatal("a class no authority can speak for must not trigger a lookup")
		return false
	}

	// Every "Cat" at the station was a crow or a cockatoo, recorded at up to
	// 0.97. Nothing can confirm a cat, so it is discarded without asking.
	discard, why := p.soundNetDiscardWith(pending("cat", 0.97, "YAMNet"), s, never)
	assert.True(t, discard, "a YAMNet-only cat at 0.97 must not be recorded")
	assert.Contains(t, why, "second opinion")
}

func TestSecondOpinionLetsAnAuthorityRescueAnAircraft(t *testing.T) {
	t.Parallel()
	p := &Processor{}
	s := secondOpinionSettings()

	// Thunder is ambiguous with aircraft, so ADS-B is asked. When it finds an
	// aeroplane overhead the detection survives - which is how most aircraft at
	// the station are found at all.
	asked := false
	overhead := func(string, time.Time, float32) bool { asked = true; return true }
	discard, _ := p.soundNetDiscardWith(pending("thunderstorm", 0.92, "YAMNet"), s, overhead)
	assert.True(t, asked, "an enrichable class must be put to the authority")
	assert.False(t, discard, "an aircraft ADS-B confirms must be kept")

	// And when nothing is overhead - the wind on the microphone - it goes.
	empty := func(string, time.Time, float32) bool { return false }
	discard, why := p.soundNetDiscardWith(pending("thunderstorm", 0.92, "YAMNet"), s, empty)
	assert.True(t, discard, "unconfirmed YAMNet-only thunder must be discarded")
	assert.Contains(t, why, "no authority confirmed")
}

func TestSecondOpinionAcceptsAnotherModelsAgreement(t *testing.T) {
	t.Parallel()
	p := &Processor{}
	s := secondOpinionSettings()

	never := func(string, time.Time, float32) bool {
		t.Fatal("a detection two models agree on is an ordinary detection")
		return false
	}
	// CED heard it too. That is the second opinion, and it clears the normal
	// threshold, so it is recorded exactly as before.
	discard, _ := p.soundNetDiscardWith(pending("cat", 0.97, "YAMNet", "CED"), s, never)
	assert.False(t, discard)

	// CED alone is not distrusted at all.
	discard, _ = p.soundNetDiscardWith(pending("cat", 0.9, "CED"), s, never)
	assert.False(t, discard)
}

// Birds and speech are not event classes, so the rule cannot touch them. This
// is what keeps it from weakening the privacy filter or losing a bird.
func TestSecondOpinionLeavesBirdsAndSpeechAlone(t *testing.T) {
	t.Parallel()
	s := secondOpinionSettings()

	for _, label := range []string{"Corvus coronoides_Australian Raven", "speech", "human_voice"} {
		assert.False(t, soundNetNeedsSecondOpinion(s, pending(label, 0.9, "YAMNet"), label),
			"%q is not an event class and must pass through", label)
	}
}

// Empty is the default and must change nothing - including on a station where
// YAMNet is the only non-bird model, which this rule would otherwise gut.
func TestSecondOpinionIsOffByDefault(t *testing.T) {
	t.Parallel()
	p := &Processor{}
	s := corroborationSettings(0.15) // no RequireSecondOpinion

	never := func(string, time.Time, float32) bool {
		t.Fatal("with the rule off a confident detection is never a candidate")
		return false
	}
	discard, _ := p.soundNetDiscardWith(pending("cat", 0.97, "YAMNet"), s, never)
	assert.False(t, discard)

	// And SoundNet itself off beats a populated list.
	off := secondOpinionSettings()
	off.SoundNet.Enabled = false
	assert.False(t, soundNetNeedsSecondOpinion(off, pending("cat", 0.97, "YAMNet"), "cat"))
}

// Model IDs arrive mixed case from the registry and lower case from YAML.
func TestSecondOpinionMatchesModelNamesCaseInsensitively(t *testing.T) {
	t.Parallel()
	s := corroborationSettings(0.15)
	s.SoundNet.Enrichment.RequireSecondOpinion = []string{" yamnet "}
	assert.True(t, soundNetNeedsSecondOpinion(s, pending("cat", 0.97, "YAMNet"), "cat"))
}

// Caught live: a Cat only CED heard, at 0.32, was discarded in YAMNet's name.
// The rule is about the distrusted model; a detection it never heard is not its
// business, however weak.
func TestSecondOpinionNeverActsOnADetectionNoDistrustedModelHeard(t *testing.T) {
	t.Parallel()
	s := secondOpinionSettings()
	cedOnly := pendingWith("cat", map[string]float64{"CED": 0.32}, "CED")
	assert.False(t, soundNetNeedsSecondOpinion(s, cedOnly, "cat"))

	p := &Processor{}
	never := func(string, time.Time, float32) bool { return false }
	discard, why := p.soundNetDiscardWith(cedOnly, s, never)
	assert.NotContains(t, why, "second opinion", "must not be blamed on YAMNet")
	_ = discard
}
