package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/bert386/soundnet-go/internal/conf"
	"github.com/bert386/soundnet-go/internal/datastore"
	"github.com/bert386/soundnet-go/internal/detection"
)

// A detection that cleared its own per-species threshold is an ordinary
// detection, not a candidate waiting on an authority. Caught on the station:
// Wind was set to 0.25, CED scored it 0.33, and it was still discarded as an
// uncorroborated "quiet" detection judged against the model's 0.7.
func TestPerSpeciesThresholdIsTheBarForCorroboration(t *testing.T) {
	t.Parallel()
	p := &Processor{}
	s := corroborationSettings(0.15)
	s.Realtime.Species.Config = map[string]conf.SpeciesConfig{"wind": {Threshold: 0.25}}

	item := &PendingDetection{
		Confidence:    0.33,
		BestModelID:   "CED",
		FirstDetected: time.Date(2026, 9, 21, 11, 0, 0, 0, time.UTC),
	}
	item.Detection.Result = detection.Result{
		RawLabel: "wind_noise_(microphone)",
		Species:  detection.Species{ScientificName: "wind", CommonName: "noise"},
	}

	never := func(string, time.Time, float32) bool {
		t.Fatal("a detection over its own threshold must not be put to an authority")
		return false
	}
	discard, _ := p.soundNetDiscardWith(item, s, never)
	assert.False(t, discard)

	// Without the per-species setting the same score is a quiet candidate again.
	s.Realtime.Species.Config = nil
	asked := false
	discard, _ = p.soundNetDiscardWith(item, s, func(string, time.Time, float32) bool { asked = true; return false })
	assert.True(t, asked)
	assert.True(t, discard)
	_ = datastore.Results{}
}
