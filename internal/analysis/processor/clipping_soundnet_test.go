package processor

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func samples(values ...int16) []byte {
	out := make([]byte, 2*len(values))
	for i, v := range values {
		binary.LittleEndian.PutUint16(out[2*i:], uint16(v))
	}
	return out
}

func TestPCMReachesFullScaleAtTheMeasuredLine(t *testing.T) {
	t.Parallel()
	// -0.1 dBFS is the line the station's clipping counts were drawn at.
	assert.False(t, pcmReachesFullScale(samples(0, 1000, -20000, 32392)), "just under the line")
	assert.True(t, pcmReachesFullScale(samples(0, 32393)), "on the line")
	assert.True(t, pcmReachesFullScale(samples(0, -32768)), "negative rail")
	assert.True(t, pcmReachesFullScale(samples(32767)), "positive rail")
	assert.False(t, pcmReachesFullScale(nil))
	assert.False(t, pcmReachesFullScale([]byte{0xff}), "a lone odd byte is not a sample")
}

func TestClippedDetectionIsNeverPutToTheAuthority(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 21, 11, 0, 0, 0, time.UTC)

	asked := false
	overhead := func(string, time.Time, float32) bool { asked = true; return true }
	var logged string

	// Clipped: refused, and ADS-B is never asked - which also saves the credit.
	wrapped := rejectingClipped(func() (bool, bool) { return true, true }, overhead,
		func(l string) { logged = l })
	assert.False(t, wrapped("thunderstorm", at, 0.92), "wind is not the aeroplane 4 km up")
	assert.False(t, asked, "a clipped detection must not spend an API credit")
	assert.Equal(t, "thunderstorm", logged)

	// Not clipped: the ordinary lookup decides.
	wrapped = rejectingClipped(func() (bool, bool) { return false, true }, overhead, nil)
	assert.True(t, wrapped("thunderstorm", at, 0.92))
	assert.True(t, asked)
}

// A missing measurement is not evidence of wind. Refusing on it would turn every
// overwritten buffer into a lost aircraft.
func TestUnknownLevelFallsThroughToTheAuthority(t *testing.T) {
	t.Parallel()
	asked := false
	overhead := func(string, time.Time, float32) bool { asked = true; return true }
	wrapped := rejectingClipped(func() (bool, bool) { return true, false }, overhead, nil)
	assert.True(t, wrapped("thunder", time.Now(), 0.8))
	assert.True(t, asked)
}

func TestRejectClippedIsOffByDefault(t *testing.T) {
	t.Parallel()
	s := corroborationSettings(0.15)
	assert.False(t, soundNetRejectsClipped(s))
	s.SoundNet.Enrichment.RejectClipped = true
	assert.True(t, soundNetRejectsClipped(s))
	s.SoundNet.Enabled = false
	assert.False(t, soundNetRejectsClipped(s))
	assert.False(t, soundNetRejectsClipped(nil))
}

// Without a buffer manager the level is unknown, never "clipped".
func TestClippedIsUnknownWithoutABuffer(t *testing.T) {
	t.Parallel()
	p := &Processor{}
	clipped, known := p.soundNetClipped(&PendingDetection{Source: "card", FirstDetected: time.Now()})
	assert.False(t, clipped)
	assert.False(t, known)
}
