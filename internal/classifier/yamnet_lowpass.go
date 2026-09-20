package classifier

// SOUNDNET: a second inference pass over a low-passed copy of the window.
//
// Aircraft at this station are quiet, low and buried under close, loud
// birdsong. 40-55% of an aircraft clip's energy sits below 250 Hz while the
// birds that mask it live above 1.5 kHz, so removing the birds leaves the
// aircraft largely intact and gives the model a window that is mostly the thing
// we want it to hear. On operator-labelled clips this lifts the aircraft
// classes from 0.374/0.646/0.367 to 0.556/0.714/0.638, and lifts three clips
// that scored exactly zero to 0.09-0.13 - still short of a useful threshold,
// but no longer invisible to a corroborating ADS-B contact.
//
// Two things about the scope are deliberate, and both come from measurement
// rather than from reasoning about what ought to help.
//
// **Only the aircraft classes take the second pass.** Low-passing raises the
// aircraft score of clips that contain no aircraft too - rustling dry grass goes
// from 0.000 to 0.137 - so the transform is only safe where the margin was
// measured, and it was measured on aircraft. Extending it to the vehicle, rail
// and watercraft domains is a plausible generalisation with no evidence behind
// it, which is exactly how the discarded normalisation step got recommended.
//
// **No normalisation.** doc/soundnet/MODEL_EVAL.md has the sweep: normalising
// after the low-pass never converts a miss into a detection and raises the worst
// non-aircraft score monotonically, from 0.137 to 0.236 uncapped. The reason is
// visible in the gain each clip asks for - clips with an aircraft need 9-13 dB,
// clips with nothing need 28-31 dB, and thirty decibels of nothing is a
// broadband rumble that sounds like an aircraft.

import (
	"math"

	"github.com/bert386/soundnet-go/internal/errors"
	"github.com/bert386/soundnet-go/internal/eventclass"
)

// lowPassCutoffHz is where the second pass cuts.
//
// 1.2 kHz sits above the fundamentals of piston and turbine engines and below
// almost all of the birdsong at this site. It is not tuned: it was the first
// value tried and it held up across the labelled set, so moving it should be
// driven by a sweep rather than by taste.
const lowPassCutoffHz = 1200.0

// biquad is one second-order section, kept as explicit state so a window can be
// filtered without allocating.
type biquad struct {
	b0, b1, b2, a1, a2 float64
	x1, x2, y1, y2     float64
}

// newLowPass returns a Butterworth low-pass section (Q = 1/sqrt(2)).
func newLowPass(sampleRate, cutoffHz float64) biquad {
	w0 := 2 * math.Pi * cutoffHz / sampleRate
	alpha := math.Sin(w0) / (2 * math.Sqrt2 / 2)
	cw := math.Cos(w0)
	a0 := 1 + alpha
	return biquad{
		b0: ((1 - cw) / 2) / a0,
		b1: (1 - cw) / a0,
		b2: ((1 - cw) / 2) / a0,
		a1: (-2 * cw) / a0,
		a2: (1 - alpha) / a0,
	}
}

// process filters src into dst. Both must be the same length; dst may alias src.
func (f *biquad) process(dst, src []float32) {
	for i, s := range src {
		x := float64(s)
		y := f.b0*x + f.b1*f.x1 + f.b2*f.x2 - f.a1*f.y1 - f.a2*f.y2
		f.x2, f.x1 = f.x1, x
		f.y2, f.y1 = f.y1, y
		dst[i] = float32(y)
	}
}

// lowPassInto writes a twice-filtered copy of src into dst.
//
// Two cascaded sections rather than one: a single second-order section rolls off
// at 12 dB/octave, which leaves too much of a loud bird at 2 kHz. Filtering
// forwards twice also shifts phase, which does not matter here because the model
// sees a magnitude spectrogram.
func lowPassInto(dst, src []float32, sampleRate float64) {
	s1 := newLowPass(sampleRate, lowPassCutoffHz)
	s2 := newLowPass(sampleRate, lowPassCutoffHz)
	s1.process(dst, src)
	s2.process(dst, dst)
}

// aircraftClasses marks the class indices the second pass is allowed to raise.
//
// Built from the taxonomy rather than from a hand-written list of names, so a
// class added to DomainAircraft is covered without anyone remembering to update
// this. Indices, not names, for the reason reportable gives: the index is the
// authoritative join and the label forms differ either side.
func aircraftClasses(numClasses int) []bool {
	out := make([]bool, numClasses)
	for _, c := range eventclass.InDomain(eventclass.DomainAircraft) {
		if c.AudioSetIndex < 0 || c.AudioSetIndex >= numClasses {
			continue
		}
		out[c.AudioSetIndex] = true
	}
	return out
}

// mergeLowPassPass runs inference a second time over a low-passed copy of the
// window and raises the aircraft classes wherever it scored higher.
//
// Per-class maximum across the two passes, matching how the four frames of a
// single pass are already combined: an aircraft that only becomes audible once
// the birds are removed should be reported at the score it earned there, and one
// that was already clear is not penalised for the filter having removed some of
// its higher harmonics.
//
// The caller holds y.mu.
func (y *YAMNet) mergeLowPassPass(clip []float32) error {
	if len(y.aircraft) != len(y.scoreBuf) {
		// Nothing to raise. Not an error: a class map without any aircraft
		// classes is a coherent configuration, just not this one.
		return nil
	}
	if cap(y.lowPassBuf) < len(clip) {
		y.lowPassBuf = make([]float32, len(clip))
	}
	buf := y.lowPassBuf[:len(clip)]
	lowPassInto(buf, clip, yamnetSampleRate)

	for _, offset := range yamnetFrameOffsets(len(clip), yamnetFrameSamples, yamnetFramesPerClip) {
		copy(y.frameBuf, buf[offset:offset+yamnetFrameSamples])

		scores, err := y.classifier.Predict(y.frameBuf)
		if err != nil {
			return err
		}
		if len(scores) != len(y.scoreBuf) {
			return errors.Newf("YAMNet low-pass pass emitted %d scores, expected %d",
				len(scores), len(y.scoreBuf)).
				Component("classifier.yamnet").
				Category(errors.CategoryModelInit).
				Build()
		}
		if idx := firstNonFinite(scores); idx != noNonFiniteScore {
			// Same reasoning as the raw pass: a NaN compares false against every
			// threshold, so it survives as a detection rather than being dropped.
			return errors.Newf("YAMNet low-pass pass returned a non-finite score at index %d", idx).
				Component("classifier.yamnet").
				Category(errors.CategoryAudio).
				Build()
		}
		for i, sc := range scores {
			if y.aircraft[i] {
				y.scoreBuf[i] = max(y.scoreBuf[i], sc)
			}
		}
	}
	return nil
}
