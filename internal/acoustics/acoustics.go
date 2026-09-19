// Package acoustics computes properties of a detection clip: how far, how
// fast, how many, how long. It is the second of the three layers in the SoundNet
// design, sitting between classification and external identification.
//
// Everything here emits measurements, never labels. A classifier says "gunshot";
// this package says "three onsets, 180 ms apart, crest factor 14". Where a
// distinction is not reliably recoverable from a single microphone - gunshot
// versus vehicle backfire being the motivating case - it exposes the features
// and raises a low-confidence flag rather than asserting an answer.
//
// All functions are deterministic and allocation-light. The performance budget
// is a Raspberry Pi 4 processing a clip in under 100 ms, so every stage is
// individually skippable and nothing here does more work than the measurement
// requires.
package acoustics

import (
	"math"
)

// SpeedOfSound is the propagation speed used throughout, in metres per second,
// at roughly 20 degrees Celsius. It is a constant rather than a temperature
// function deliberately: the ~0.6 m/s per degree variation is far below the
// error in every measurement that consumes it.
const SpeedOfSound = 343.0

// Document is the diagnostics result for one clip. Every section is optional:
// a stage that did not run, or could not produce a trustworthy answer, leaves
// its pointer nil rather than reporting a zero value that reads like a
// measurement.
type Document struct {
	// SchemaVersion identifies this document's shape for stored payloads.
	SchemaVersion int `json:"schema_version"`

	Level    *LevelResult    `json:"level,omitempty"`
	Onsets   *OnsetResult    `json:"onsets,omitempty"`
	Envelope *EnvelopeResult `json:"envelope,omitempty"`
	Doppler  *DopplerResult  `json:"doppler,omitempty"`
	Impulse  *ImpulseResult  `json:"impulse,omitempty"`

	// Warnings records why a stage declined to produce an answer. Surfacing this
	// is the point: "no Doppler result" and "Doppler attempted but the clip had
	// no usable tonal component" mean very different things to an operator.
	Warnings []string `json:"warnings,omitempty"`
}

// SchemaVersion is the current Document shape.
const SchemaVersion = 1

// LevelResult describes loudness and spectral shape.
//
// Distance is reported as a relative indicator, not metres. Air absorbs high
// frequencies faster than low ones, so a distant source arrives duller: the
// spectral tilt carries distance information that raw level alone cannot,
// because level also varies with how loud the source was to begin with.
// Converting either into absolute metres requires a calibrated microphone and
// a known source level, which is why AbsoluteDistanceM is only populated when
// the configuration declares a calibrated capture chain.
type LevelResult struct {
	// PeakDBFS and RMSDBFS are relative to digital full scale, so they are
	// comparable between clips from the same capture chain but carry no absolute
	// sound pressure meaning without calibration.
	PeakDBFS float64 `json:"peak_dbfs"`
	RMSDBFS  float64 `json:"rms_dbfs"`

	// SpectralTiltDBPerDecade is the slope of band level against log frequency.
	// More negative means high frequencies have fallen off more, which for a
	// given source type suggests a more distant or more occluded source.
	SpectralTiltDBPerDecade float64 `json:"spectral_tilt_db_per_decade"`

	// Proximity is a coarse, honest summary of the tilt: "near", "mid" or "far".
	// It is explicitly relative and site-dependent, which is why it is a word
	// rather than a number pretending to precision it does not have.
	Proximity string `json:"proximity"`

	// AbsoluteDistanceM is populated only with a calibrated microphone and a
	// known reference source level. Nil otherwise, rather than a guess.
	AbsoluteDistanceM *float64 `json:"absolute_distance_m,omitempty"`

	// BandCount is how many octave bands contributed to the tilt estimate. A
	// tilt from two bands is not worth the same trust as one from twenty.
	BandCount int `json:"band_count"`
}

// Proximity buckets. Thresholds are configurable because they depend on the
// site, the microphone and the source; these names are comparative, never
// absolute claims about distance.
const (
	ProximityNear    = "near"
	ProximityMid     = "mid"
	ProximityFar     = "far"
	ProximityUnknown = "unknown"
)

// OnsetResult counts discrete transients. This is what turns "a gunshot was
// detected" into "three shots were fired", and it is the same machinery that
// counts hammer blows and nail-gun reports.
type OnsetResult struct {
	Count int `json:"count"`

	// TimesSec are onset positions from the start of the clip.
	TimesSec []float64 `json:"times_sec"`

	// IntervalStats summarise the gaps between onsets. Regular intervals suggest
	// a machine (a nail gun, a saw stroke); irregular ones suggest a person.
	MeanIntervalSec   float64 `json:"mean_interval_sec,omitempty"`
	StdDevIntervalSec float64 `json:"stddev_interval_sec,omitempty"`

	// Regular reports whether intervals are consistent enough to suggest a
	// mechanical source. Useful for separating a nail gun from gunfire.
	Regular bool `json:"regular"`
}

// EnvelopeResult measures how long an event took to decay. Thunder is the
// motivating case: a nearby strike is a sharp crack, a distant one is a long
// rumble, because the sound arrives from different parts of the channel at
// different times.
type EnvelopeResult struct {
	// AttackSec is onset to peak; DecaySec is peak to the decay threshold.
	AttackSec float64 `json:"attack_sec"`
	DecaySec  float64 `json:"decay_sec"`

	// DurationSec is the total span above the decay threshold.
	DurationSec float64 `json:"duration_sec"`
}

// DopplerResult is a pass-by estimate. It exists only for sources that actually
// pass the microphone: a stationary idling engine produces no frequency shift
// and therefore no speed.
type DopplerResult struct {
	// SpeedKmh is the source's speed derived from the frequency ratio either
	// side of the pass-by.
	SpeedKmh float64 `json:"speed_kmh"`

	// CPAMetres is the closest point of approach: how far away the source was at
	// its nearest. Derived from how abruptly the frequency swept, which is
	// steeper for a closer pass.
	CPAMetres float64 `json:"cpa_metres"`

	// CPATimeSec is when the source was closest, relative to the clip start.
	CPATimeSec float64 `json:"cpa_time_sec"`

	// RestFrequencyHz is the emitted frequency with the shift removed.
	RestFrequencyHz float64 `json:"rest_frequency_hz"`

	// Confidence is how well the observed track matched a pass-by shape, 0..1.
	// A low value means the numbers above should not be trusted; it is reported
	// rather than used to silently suppress the result, so an operator tuning
	// thresholds can see what the fit actually did.
	Confidence float64 `json:"confidence"`
}

// ImpulseResult holds the shape features of a transient.
//
// These exist to inform a judgement, not to make one. Gunshot versus vehicle
// backfire is not reliably separable from a single microphone: both are short,
// loud and broadband. The features are exposed and LowConfidence is set, so the
// UI can present the pair for human review instead of asserting an answer the
// data does not support.
type ImpulseResult struct {
	// CrestFactor is peak over RMS. A true impulse has a high crest factor; a
	// sustained sound does not.
	CrestFactor float64 `json:"crest_factor"`

	// RiseTimeMs is 10% to 90% of peak amplitude. Gunshots rise faster than
	// backfires in principle, but the difference is small and distance-dependent,
	// which is exactly why this is a feature and not a verdict.
	RiseTimeMs float64 `json:"rise_time_ms"`

	// SpectralCentroidHz is the energy-weighted mean frequency: a brightness
	// measure that falls with distance.
	SpectralCentroidHz float64 `json:"spectral_centroid_hz"`

	// LowConfidence marks a measurement that must not be presented as an answer.
	// Set whenever the features fall in the region where impulse classes overlap.
	LowConfidence bool `json:"low_confidence"`

	// Ambiguity names why confidence is low, for display next to the result.
	Ambiguity string `json:"ambiguity,omitempty"`
}

// rms returns the root-mean-square of a signal.
func rms(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	var sum float64
	for _, v := range x {
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(x)))
}

// peakAbs returns the largest absolute sample value.
func peakAbs(x []float64) float64 {
	var p float64
	for _, v := range x {
		if a := math.Abs(v); a > p {
			p = a
		}
	}
	return p
}

// dbfs converts a linear amplitude to decibels relative to full scale.
// Silence maps to a floor rather than negative infinity so that downstream
// arithmetic and JSON encoding stay well defined.
func dbfs(amplitude float64) float64 {
	const floor = -120.0
	if amplitude <= 0 {
		return floor
	}
	db := 20 * math.Log10(amplitude)
	if db < floor {
		return floor
	}
	return db
}

// mean returns the arithmetic mean, or 0 for an empty slice.
func mean(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	var s float64
	for _, v := range x {
		s += v
	}
	return s / float64(len(x))
}

// stdDev returns the population standard deviation.
func stdDev(x []float64) float64 {
	if len(x) < 2 {
		return 0
	}
	m := mean(x)
	var s float64
	for _, v := range x {
		d := v - m
		s += d * d
	}
	return math.Sqrt(s / float64(len(x)))
}

// median returns the median, sorting a copy so the caller's slice is untouched.
func median(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	c := make([]float64, len(x))
	copy(c, x)
	insertionSort(c)
	n := len(c)
	if n%2 == 1 {
		return c[n/2]
	}
	return (c[n/2-1] + c[n/2]) / 2
}

// insertionSort is used instead of sort.Float64s to keep this package free of
// allocation surprises on small slices; envelope frame counts are in the
// hundreds, where insertion sort is competitive and entirely predictable.
func insertionSort(a []float64) {
	for i := 1; i < len(a); i++ {
		v := a[i]
		j := i - 1
		for j >= 0 && a[j] > v {
			a[j+1] = a[j]
			j--
		}
		a[j+1] = v
	}
}
