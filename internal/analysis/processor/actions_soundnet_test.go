package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bert386/soundnet-go/internal/eventpipeline"
)

// TestSoundNetActionIsNilUntilConfigured guards the opt-in rule: with no
// analyser installed, nothing is appended to the action list at all.
func TestSoundNetActionIsNilUntilConfigured(t *testing.T) {
	p := &Processor{}
	det := &Detections{pcmData3s: make([]byte, 96000)}
	assert.Nil(t, p.buildSoundNetAction(det, &DetectionContext{}))
}

// TestSoundNetActionNeedsAClip checks the other guard: no audio, no action.
func TestSoundNetActionNeedsAClip(t *testing.T) {
	ConfigureSoundNet(&eventpipeline.Analyser{Config: eventpipeline.DefaultConfig()})
	p := &Processor{}
	assert.Nil(t, p.buildSoundNetAction(&Detections{}, &DetectionContext{}),
		"a detection with no PCM has nothing to analyse")
}

// TestSoundNetActionBuildsWhenConfigured is the one that matters.
//
// An earlier version of the integration appended the action from a deferred
// closure, which silently did nothing because getDefaultActions returns an
// unnamed slice - it compiled and ran and produced no action at all. This
// asserts the action is really constructed, so that failure mode cannot recur
// unnoticed.
func TestSoundNetActionBuildsWhenConfigured(t *testing.T) {
	ConfigureSoundNet(&eventpipeline.Analyser{Config: eventpipeline.DefaultConfig()})
	p := &Processor{}
	det := &Detections{CorrelationID: "abc", pcmData3s: make([]byte, 96000)}
	det.Result.RawLabel = "Gunshot, gunfire"
	det.Result.Confidence = 0.9

	action := p.buildSoundNetAction(det, &DetectionContext{})
	require.NotNil(t, action, "the action must be built when SoundNet is configured and a clip exists")

	sn, ok := action.(*SoundNetAction)
	require.True(t, ok)
	assert.Equal(t, "Gunshot, gunfire", sn.Label,
		"the raw classifier label is used, not the localised display name")
	assert.NotEmpty(t, sn.GetDescription())
}

// TestSoundNetLabelPrefersRawLabel documents why: display names are localised
// and normalised, and a rewritten label would resolve to no event class at all.
func TestSoundNetLabelPrefersRawLabel(t *testing.T) {
	var r struct{ raw, common, want string }
	for _, r = range []struct{ raw, common, want string }{
		{"Gunshot, gunfire", "Gunshot", "Gunshot, gunfire"},
		{"", "Common Blackbird", "Common Blackbird"},
	} {
		det := &Detections{}
		det.Result.RawLabel = r.raw
		det.Result.Species.CommonName = r.common
		assert.Equal(t, r.want, soundNetLabel(&det.Result))
	}
}
