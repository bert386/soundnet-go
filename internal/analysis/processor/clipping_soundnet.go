package processor

// SOUNDNET: a sound that overloads the microphone is not a distant aircraft.
//
// Corroboration keeps an ambiguous detection when ADS-B finds an aircraft in
// range. At the deployment station that range is 3 to 6 km for nearly every
// match - the sky is full of airliners at about 3,000 m - so the question was
// never really "how far away is the aeroplane", and a distance limit could not
// answer it: rows rescued from Thunder sit at exactly the same distances as
// rows the classifier called an aircraft outright.
//
// What does separate them is the level. Joined against the stored diagnostics,
// every ADS-B match on the station so far:
//
//	heard as aircraft    2 of 108 clipped   (0 of 20 within 2 km)
//	heard as thunder    39 of 167 clipped
//	heard as vehicle    10 of 247 clipped
//
// Real aircraft essentially never clip this microphone, not even the ones
// overhead within two kilometres. Wind on the capsule clips it most of the time
// - 73% of the Thunder rows nothing identified. So a clipped detection is not
// put to ADS-B at all: whatever it was, it was not the aeroplane that happened
// to be four kilometres up at the time. It also saves the credit.

import (
	"encoding/binary"
	"time"

	"github.com/bert386/soundnet-go/internal/conf"
	"github.com/bert386/soundnet-go/internal/logger"
)

const (
	// soundNetClipLevel is -0.1 dBFS on a 16-bit sample: 32768 * 10^(-0.1/20).
	// The same line the measurement above was drawn at (peak_dbfs >= -0.1), so
	// the rule removes what was counted and nothing else.
	soundNetClipLevel = 32393

	// The window examined: one second before the detection and four after,
	// five in all - the length the diagnostics engine analyses (maxclipms 5000
	// on the station), so a detection is judged on the same audio its stored
	// peak level came from.
	soundNetClipLead  = time.Second
	soundNetClipTrail = 4 * time.Second

	// soundNetClipGuard keeps the window clear of the capture write head,
	// which ReadSegment will not wait for. See captureGuard in the auto-label
	// wiring for the same race.
	soundNetClipGuard = 250 * time.Millisecond
)

// corroborateFunc is the authority lookup soundNetDiscardWith is given.
type corroborateFunc func(label string, at time.Time, confidence float32) bool

// soundNetRejectsClipped reports whether the operator has asked for clipped
// detections never to be confirmed. Off by default.
func soundNetRejectsClipped(settings *conf.Settings) bool {
	return settings != nil && settings.SoundNet.Enabled && settings.SoundNet.Enrichment.RejectClipped
}

// rejectingClipped wraps a lookup so that a clipped detection is refused before
// anyone is asked.
//
// clipped reports (clipped, known). Unknown - no buffer, a window the buffer
// has already overwritten - falls through to the ordinary lookup: a missing
// measurement is not evidence of wind, and refusing on it would quietly turn
// every buffer hiccup into lost aircraft.
func rejectingClipped(clipped func() (bool, bool), next corroborateFunc, log func(label string)) corroborateFunc {
	return func(label string, at time.Time, confidence float32) bool {
		if isClipped, known := clipped(); known && isClipped {
			if log != nil {
				log(label)
			}
			return false
		}
		return next(label, at, confidence)
	}
}

// soundNetClipped reads the detection's audio back from the capture buffer and
// reports whether it reached full scale.
func (p *Processor) soundNetClipped(item *PendingDetection) (clipped, known bool) {
	if p == nil || p.BufferMgr == nil || item == nil || item.Source == "" {
		return false, false
	}
	cb, err := p.BufferMgr.CaptureBuffer(item.Source)
	if err != nil || cb == nil {
		return false, false
	}
	start := item.FirstDetected.Add(-soundNetClipLead)
	end := item.FirstDetected.Add(soundNetClipTrail)
	if latest := time.Now().Add(-soundNetClipGuard); end.After(latest) {
		end = latest
	}
	if !end.After(start) {
		return false, false
	}
	pcm, err := cb.ReadSegment(start, end)
	if err != nil || len(pcm) < 2 {
		return false, false
	}
	return pcmReachesFullScale(pcm), true
}

// pcmReachesFullScale scans 16-bit little-endian samples, the capture buffer's
// fixed format, for one at or beyond the clip level. One sample is enough: a
// clipped waveform is flattened against the rail for many, and a single
// full-scale sample on this microphone has only ever meant overload.
func pcmReachesFullScale(pcm []byte) bool {
	for i := 0; i+1 < len(pcm); i += 2 {
		v := int(int16(binary.LittleEndian.Uint16(pcm[i:])))
		if v >= soundNetClipLevel || v <= -soundNetClipLevel {
			return true
		}
	}
	return false
}

// logClipRejection is the info-level record of a refusal. At info because each
// one is a detection the station would have kept as an aircraft yesterday.
func (p *Processor) logClipRejection(item *PendingDetection) func(label string) {
	return func(label string) {
		GetLogger().Info("soundnet: not confirming a clipped detection as an aircraft",
			logger.String("label", label),
			logger.Float64("confidence", item.Confidence),
			logger.String("source", p.getDisplayNameForSource(item.Source)),
			logger.String("operation", "soundnet_clipped"))
	}
}
