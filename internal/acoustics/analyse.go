package acoustics

import (
	"math"
)

// LevelConfig tunes the level and near/far stage.
type LevelConfig struct {
	// TiltNearMax and TiltFarMin bracket the "mid" proximity bucket, in
	// dB/decade. Defaults suit a general outdoor site; they are configurable
	// because the right values depend on the microphone and the surroundings.
	TiltNearMax float64
	TiltFarMin  float64

	// MinBands is the fewest octave bands that can support a tilt estimate.
	// Below this the slope is noise and Proximity reports "unknown".
	MinBands int

	// Calibrated declares that the capture chain has a known sensitivity, which
	// is the only circumstance under which an absolute distance is meaningful.
	Calibrated bool

	// ReferenceSPLAt1m is the source's expected level one metre away, in dB.
	// Used only when Calibrated is set.
	ReferenceSPLAt1m float64
}

// DefaultLevelConfig returns conservative defaults.
func DefaultLevelConfig() LevelConfig {
	return LevelConfig{
		TiltNearMax: -6,
		TiltFarMin:  -14,
		MinBands:    4,
	}
}

// Band is one 1/3-octave measurement, mirroring the shape upstream's sound level
// monitor already produces so its output can be fed straight in.
type Band struct {
	CenterFreqHz float64
	LevelDB      float64
}

// AnalyseLevel computes loudness and the high-frequency tilt that indicates
// relative distance.
//
// The tilt is a least-squares slope of band level against log10 frequency,
// restricted to bands above 500 Hz. The restriction matters: atmospheric
// absorption is negligible at low frequencies, so including them flattens the
// slope and dilutes exactly the signal being measured.
//
// Bands are the reused output of the existing 1/3-octave monitor rather than a
// fresh FFT, which is both cheaper on a Pi and what the scope asks for.
func AnalyseLevel(samples []float64, bands []Band, cfg LevelConfig) *LevelResult {
	res := &LevelResult{
		PeakDBFS:  dbfs(peakAbs(samples)),
		RMSDBFS:   dbfs(rms(samples)),
		Proximity: ProximityUnknown,
	}

	const tiltFloorHz = 500
	var xs, ys []float64
	for _, b := range bands {
		if b.CenterFreqHz >= tiltFloorHz && !math.IsInf(b.LevelDB, 0) && !math.IsNaN(b.LevelDB) {
			xs = append(xs, math.Log10(b.CenterFreqHz))
			ys = append(ys, b.LevelDB)
		}
	}
	res.BandCount = len(xs)

	if len(xs) < cfg.MinBands {
		// Too few bands to distinguish a slope from noise. Reporting "unknown"
		// is the honest outcome; a slope fitted through two points is not a
		// measurement.
		return res
	}

	res.SpectralTiltDBPerDecade = linearSlope(xs, ys)
	switch {
	case res.SpectralTiltDBPerDecade >= cfg.TiltNearMax:
		res.Proximity = ProximityNear
	case res.SpectralTiltDBPerDecade <= cfg.TiltFarMin:
		res.Proximity = ProximityFar
	default:
		res.Proximity = ProximityMid
	}

	if cfg.Calibrated && cfg.ReferenceSPLAt1m > 0 {
		// Inverse-square spreading: 6 dB per doubling of distance. Only valid
		// with a calibrated chain and a known source level, hence the guard.
		lossDB := cfg.ReferenceSPLAt1m - res.RMSDBFS
		if lossDB > 0 {
			d := math.Pow(10, lossDB/20)
			res.AbsoluteDistanceM = &d
		}
	}
	return res
}

// linearSlope returns the least-squares slope of y against x.
func linearSlope(xs, ys []float64) float64 {
	n := float64(len(xs))
	if n < 2 {
		return 0
	}
	mx, my := mean(xs), mean(ys)
	var num, den float64
	for i := range xs {
		dx := xs[i] - mx
		num += dx * (ys[i] - my)
		den += dx * dx
	}
	if den == 0 {
		return 0
	}
	return num / den
}

// OnsetConfig tunes transient detection.
type OnsetConfig struct {
	// FrameSec is the envelope analysis hop. Smaller resolves closer events at
	// proportionally more cost.
	FrameSec float64

	// RefractorySec is the dead time after an onset during which another cannot
	// be reported. This is what stops a single gunshot's echo, or its own decay
	// ringing, being counted as a second shot - without it, counts in a reverberant
	// space are wildly inflated.
	RefractorySec float64

	// ThresholdMAD is how many median-absolute-deviations above the median
	// envelope a peak must rise. An adaptive threshold rather than an absolute
	// one, so a quiet clip and a loud clip both work.
	ThresholdMAD float64

	// RegularityTolerance is the coefficient of variation below which intervals
	// count as regular, suggesting a mechanical source.
	RegularityTolerance float64
}

// DefaultOnsetConfig returns defaults suited to gunshots and hammer blows.
func DefaultOnsetConfig() OnsetConfig {
	return OnsetConfig{
		FrameSec:            0.005,
		RefractorySec:       0.08,
		ThresholdMAD:        6,
		RegularityTolerance: 0.15,
	}
}

// AnalyseOnsets counts discrete transients using an adaptive threshold and a
// refractory period.
func AnalyseOnsets(samples []float64, sampleRate int, cfg OnsetConfig) *OnsetResult {
	if sampleRate <= 0 || len(samples) == 0 {
		return &OnsetResult{}
	}
	env, hop := envelopeFrames(samples, sampleRate, cfg.FrameSec)
	if len(env) < 3 {
		return &OnsetResult{}
	}

	// Threshold from the median and the median absolute deviation. Both are
	// robust to the outliers we are hunting: using a mean and standard deviation
	// here would let a loud transient raise the very threshold meant to catch it.
	med := median(env)
	devs := make([]float64, len(env))
	for i, v := range env {
		devs[i] = math.Abs(v - med)
	}
	mad := median(devs)
	if mad <= 0 {
		mad = 1e-9
	}
	threshold := med + cfg.ThresholdMAD*mad

	refractoryFrames := int(cfg.RefractorySec / hop)
	if refractoryFrames < 1 {
		refractoryFrames = 1
	}

	res := &OnsetResult{}
	lastOnset := -refractoryFrames - 1
	for i := 0; i < len(env); i++ {
		// A peak needs to dominate its neighbours, but the first and last frames
		// have only one neighbour each. Treating the missing side as -inf rather
		// than skipping those frames matters: clips are routinely cut to begin at
		// the event, so requiring a preceding frame would miss the very onset the
		// clip was extracted for.
		risingEdge := i == 0 || env[i] >= env[i-1]
		fallingEdge := i == len(env)-1 || env[i] > env[i+1]
		isPeak := env[i] > threshold && risingEdge && fallingEdge
		if isPeak && i-lastOnset > refractoryFrames {
			res.TimesSec = append(res.TimesSec, float64(i)*hop)
			lastOnset = i
		}
	}
	res.Count = len(res.TimesSec)

	if res.Count >= 2 {
		intervals := make([]float64, 0, res.Count-1)
		for i := 1; i < res.Count; i++ {
			intervals = append(intervals, res.TimesSec[i]-res.TimesSec[i-1])
		}
		res.MeanIntervalSec = mean(intervals)
		res.StdDevIntervalSec = stdDev(intervals)
		if res.MeanIntervalSec > 0 {
			cv := res.StdDevIntervalSec / res.MeanIntervalSec
			res.Regular = cv <= cfg.RegularityTolerance
		}
	}
	return res
}

// envelopeFrames returns a frame-wise RMS envelope and the hop size in seconds.
func envelopeFrames(samples []float64, sampleRate int, frameSec float64) (env []float64, hopSec float64) {
	frameLen := int(frameSec * float64(sampleRate))
	if frameLen < 1 {
		frameLen = 1
	}
	n := len(samples) / frameLen
	env = make([]float64, 0, n)
	for i := 0; i+frameLen <= len(samples); i += frameLen {
		env = append(env, rms(samples[i:i+frameLen]))
	}
	return env, float64(frameLen) / float64(sampleRate)
}

// AnalyseEnvelope measures attack and decay around the loudest point.
//
// decayDropDB is how far below the peak counts as decayed; 20 dB is a
// conventional choice, being where a transient has lost 99% of its power.
func AnalyseEnvelope(samples []float64, sampleRate int, frameSec, decayDropDB float64) *EnvelopeResult {
	if sampleRate <= 0 || len(samples) == 0 {
		return nil
	}
	env, hop := envelopeFrames(samples, sampleRate, frameSec)
	if len(env) < 2 {
		return nil
	}

	peakIdx, peakVal := 0, 0.0
	for i, v := range env {
		if v > peakVal {
			peakVal, peakIdx = v, i
		}
	}
	if peakVal <= 0 {
		return nil
	}
	floor := peakVal * math.Pow(10, -decayDropDB/20)

	start := peakIdx
	for start > 0 && env[start-1] > floor {
		start--
	}
	end := peakIdx
	for end < len(env)-1 && env[end+1] > floor {
		end++
	}

	return &EnvelopeResult{
		AttackSec:   float64(peakIdx-start) * hop,
		DecaySec:    float64(end-peakIdx) * hop,
		DurationSec: float64(end-start) * hop,
	}
}

// ImpulseConfig tunes impulse shape analysis.
type ImpulseConfig struct {
	// MinCrestFactor is the crest factor below which a sound is not impulsive
	// enough for these features to mean anything.
	MinCrestFactor float64

	// AmbiguousRiseMinMs and AmbiguousRiseMaxMs bracket the rise times where
	// gunshots and backfires overlap. Inside this window the result is flagged
	// low-confidence.
	AmbiguousRiseMinMs float64
	AmbiguousRiseMaxMs float64
}

// DefaultImpulseConfig returns defaults for the gunshot/backfire problem.
func DefaultImpulseConfig() ImpulseConfig {
	return ImpulseConfig{
		MinCrestFactor:     6,
		AmbiguousRiseMinMs: 0.5,
		AmbiguousRiseMaxMs: 8,
	}
}

// AnalyseImpulse extracts transient shape features.
//
// It deliberately never returns a class. The scope is explicit that gunshot
// versus backfire cannot be settled from a single microphone, so this reports
// measurements and marks ambiguity for a human to resolve.
func AnalyseImpulse(samples []float64, sampleRate int, cfg ImpulseConfig) *ImpulseResult {
	if sampleRate <= 0 || len(samples) == 0 {
		return nil
	}
	peak := peakAbs(samples)
	r := rms(samples)
	if r <= 0 || peak <= 0 {
		return nil
	}

	res := &ImpulseResult{CrestFactor: peak / r}
	res.RiseTimeMs = riseTimeMs(samples, sampleRate, peak)
	res.SpectralCentroidHz = spectralCentroid(samples, sampleRate)

	// Impulse shape is always reported as low-confidence, and the reason says
	// which case applies. This is not caution for its own sake: propagation
	// smears rise time with distance and reflections, so a sharp rise is
	// evidence for a gunshot but never proof against a nearby backfire. An
	// earlier version flagged only a middle band of rise times, which had the
	// effect of implicitly asserting "gunshot" for anything sharper - exactly
	// the claim the scope says the data cannot support.
	res.LowConfidence = true
	switch {
	case res.CrestFactor < cfg.MinCrestFactor:
		res.Ambiguity = "not impulsive enough for shape features to discriminate"
	case res.RiseTimeMs >= cfg.AmbiguousRiseMinMs && res.RiseTimeMs <= cfg.AmbiguousRiseMaxMs:
		res.Ambiguity = "rise time overlaps gunshot and vehicle backfire; not separable from one microphone"
	case res.RiseTimeMs < cfg.AmbiguousRiseMinMs:
		res.Ambiguity = "very fast rise is consistent with a gunshot, but distance and reflections can sharpen a backfire too"
	default:
		res.Ambiguity = "slow rise leans away from a gunshot, but a distant shot can arrive blunted"
	}
	return res
}

// riseTimeMs measures 10% to 90% of peak on the rising edge before the peak.
func riseTimeMs(samples []float64, sampleRate int, peak float64) float64 {
	peakIdx := 0
	for i, v := range samples {
		if math.Abs(v) >= peak {
			peakIdx = i
			break
		}
	}
	lo, hi := 0.1*peak, 0.9*peak
	loIdx, hiIdx := -1, -1
	for i := peakIdx; i >= 0; i-- {
		a := math.Abs(samples[i])
		if hiIdx == -1 && a <= hi {
			hiIdx = i
		}
		if a <= lo {
			loIdx = i
			break
		}
	}
	if loIdx == -1 || hiIdx == -1 || hiIdx <= loIdx {
		return 0
	}
	return float64(hiIdx-loIdx) / float64(sampleRate) * 1000
}
