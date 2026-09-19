package acoustics

import (
	"math"
)

// fftRadix2 computes an in-place complex FFT. n must be a power of two.
//
// A local implementation rather than a dependency: the module has no FFT
// library, and a fork that must stay mergeable should not add one for eighty
// lines of standard algorithm. It is iterative to avoid recursion overhead on
// a Raspberry Pi.
func fftRadix2(re, im []float64) {
	n := len(re)
	if n <= 1 || n&(n-1) != 0 {
		return
	}
	// Bit-reversal permutation.
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j |= bit
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for length := 2; length <= n; length <<= 1 {
		ang := -2 * math.Pi / float64(length)
		wRe, wIm := math.Cos(ang), math.Sin(ang)
		for i := 0; i < n; i += length {
			curRe, curIm := 1.0, 0.0
			half := length / 2
			for j := 0; j < half; j++ {
				uRe, uIm := re[i+j], im[i+j]
				vRe := re[i+j+half]*curRe - im[i+j+half]*curIm
				vIm := re[i+j+half]*curIm + im[i+j+half]*curRe
				re[i+j], im[i+j] = uRe+vRe, uIm+vIm
				re[i+j+half], im[i+j+half] = uRe-vRe, uIm-vIm
				curRe, curIm = curRe*wRe-curIm*wIm, curRe*wIm+curIm*wRe
			}
		}
	}
}

// magnitudeSpectrum returns the magnitude spectrum of one frame, Hann-windowed
// and zero-padded to the next power of two.
func magnitudeSpectrum(frame []float64) []float64 {
	n := 1
	for n < len(frame) {
		n <<= 1
	}
	re := make([]float64, n)
	im := make([]float64, n)
	for i, v := range frame {
		// Hann window: without it, frame edges leak energy across the spectrum
		// and smear the very peak the Doppler tracker is trying to follow.
		w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(len(frame)-1))
		re[i] = v * w
	}
	fftRadix2(re, im)
	mag := make([]float64, n/2)
	for i := range mag {
		mag[i] = math.Hypot(re[i], im[i])
	}
	return mag
}

// spectralCentroid returns the energy-weighted mean frequency of a signal.
func spectralCentroid(samples []float64, sampleRate int) float64 {
	if len(samples) == 0 || sampleRate <= 0 {
		return 0
	}
	mag := magnitudeSpectrum(samples)
	binHz := float64(sampleRate) / float64(len(mag)*2)
	var num, den float64
	for i, m := range mag {
		num += float64(i) * binHz * m
		den += m
	}
	if den == 0 {
		return 0
	}
	return num / den
}

// DopplerConfig tunes pass-by analysis.
type DopplerConfig struct {
	// FrameSec is the analysis window. Long enough for frequency resolution,
	// short enough to follow the sweep.
	FrameSec float64

	// MinFreqHz and MaxFreqHz bracket where a vehicle's dominant tone is
	// expected. Restricting the search stops the tracker locking onto wind
	// rumble below, or tyre hiss above, instead of the engine tone.
	MinFreqHz float64
	MaxFreqHz float64

	// MinTrackFrames is the fewest usable frames for a fit to be attempted.
	MinTrackFrames int

	// MinRatio is the smallest before/after frequency ratio treated as a real
	// pass-by. Below this the source was probably stationary, or passed too far
	// away to shift measurably.
	MinRatio float64
}

// DefaultDopplerConfig returns defaults for road vehicles.
func DefaultDopplerConfig() DopplerConfig {
	return DopplerConfig{
		FrameSec:       0.064,
		MinFreqHz:      80,
		MaxFreqHz:      2000,
		MinTrackFrames: 8,
		MinRatio:       1.02,
	}
}

// AnalyseDoppler estimates a pass-by's speed and closest point of approach.
//
// The physics: approaching, the emitted frequency f0 is heard raised to
// f0*c/(c-v); receding, lowered to f0*c/(c+v). The ratio of the two plateaus
// therefore gives speed without needing to know f0:
//
//	r = f_before/f_after = (c+v)/(c-v)  =>  v = c*(r-1)/(r+1)
//
// Closest approach follows from how abruptly the sweep happened. The frequency
// slope at the crossing is steepest for a close pass and gentle for a distant
// one, and for a straight-line pass-by at constant speed:
//
//	|df/dt|max = f0*v^2/(c*d)  =>  d = f0*v^2/(c*|df/dt|max)
//
// This closed form is used rather than a nonlinear least-squares fit of the full
// curve, because it is far cheaper on a Pi, it has no convergence failure mode,
// and its assumptions are visible. It returns nil rather than a number whenever
// the clip does not actually contain a pass-by.
func AnalyseDoppler(samples []float64, sampleRate int, cfg DopplerConfig) (*DopplerResult, string) {
	if sampleRate <= 0 || len(samples) == 0 {
		return nil, "no audio"
	}
	frameLen := int(cfg.FrameSec * float64(sampleRate))
	if frameLen < 32 || len(samples) < frameLen*cfg.MinTrackFrames {
		return nil, "clip too short for a pass-by fit"
	}

	// Track the dominant frequency frame by frame.
	var times, freqs []float64
	hop := frameLen / 2 // 50% overlap: smooths the track without much extra cost
	for start := 0; start+frameLen <= len(samples); start += hop {
		f := dominantFrequency(samples[start:start+frameLen], sampleRate, cfg.MinFreqHz, cfg.MaxFreqHz)
		if f > 0 {
			times = append(times, float64(start+frameLen/2)/float64(sampleRate))
			freqs = append(freqs, f)
		}
	}
	if len(freqs) < cfg.MinTrackFrames {
		return nil, "no usable tonal component to track"
	}

	// Plateaus either side of the sweep. The first and last thirds avoid the
	// transition itself, where the frequency is mid-sweep and represents neither
	// the approaching nor the receding value.
	third := len(freqs) / 3
	if third < 1 {
		return nil, "track too short to separate approach from recession"
	}
	fBefore := median(freqs[:third])
	fAfter := median(freqs[len(freqs)-third:])
	if fAfter <= 0 {
		return nil, "invalid frequency track"
	}

	ratio := fBefore / fAfter
	if ratio < cfg.MinRatio {
		// No meaningful shift. A stationary source, or one that never came close
		// enough for its geometry to change. Reporting nothing is correct here;
		// a speed computed from noise would look exactly like a measurement.
		return nil, "no Doppler shift detected; source was stationary or distant"
	}

	v := SpeedOfSound * (ratio - 1) / (ratio + 1)
	f0 := math.Sqrt(fBefore * fAfter) // geometric mean removes the shift

	// Steepest downward slope marks the crossing.
	maxSlope, cpaTime := 0.0, times[len(times)/2]
	for i := 1; i < len(freqs); i++ {
		dt := times[i] - times[i-1]
		if dt <= 0 {
			continue
		}
		slope := (freqs[i] - freqs[i-1]) / dt
		if slope < maxSlope {
			maxSlope = slope
			cpaTime = (times[i] + times[i-1]) / 2
		}
	}

	res := &DopplerResult{
		SpeedKmh:        v * 3.6,
		CPATimeSec:      cpaTime,
		RestFrequencyHz: f0,
	}
	if maxSlope < 0 {
		res.CPAMetres = f0 * v * v / (SpeedOfSound * math.Abs(maxSlope))
	}

	// Confidence from how cleanly the track splits into two plateaus. A genuine
	// pass-by is flat, swept, flat; noise wanders throughout.
	res.Confidence = passByConfidence(freqs, third)
	return res, ""
}

// dominantFrequency returns the strongest frequency in a band, refined by
// parabolic interpolation across the peak bin.
//
// The interpolation matters more than it might appear: at a 64 ms frame the raw
// bin spacing is around 16 Hz, while the whole Doppler shift for a car at 70 km/h
// is only about 11% of the emitted frequency. Without sub-bin refinement the
// speed estimate would quantise into useless steps.
func dominantFrequency(frame []float64, sampleRate int, minHz, maxHz float64) float64 {
	mag := magnitudeSpectrum(frame)
	if len(mag) < 3 {
		return 0
	}
	binHz := float64(sampleRate) / float64(len(mag)*2)
	lo := int(minHz / binHz)
	hi := int(maxHz / binHz)
	if lo < 1 {
		lo = 1
	}
	if hi >= len(mag)-1 {
		hi = len(mag) - 2
	}
	if lo >= hi {
		return 0
	}
	best, bestIdx := 0.0, -1
	for i := lo; i <= hi; i++ {
		if mag[i] > best {
			best, bestIdx = mag[i], i
		}
	}
	if bestIdx < 1 {
		return 0
	}
	alpha, beta, gamma := mag[bestIdx-1], mag[bestIdx], mag[bestIdx+1]
	denom := alpha - 2*beta + gamma
	offset := 0.0
	if denom != 0 {
		offset = 0.5 * (alpha - gamma) / denom
	}
	return (float64(bestIdx) + offset) * binHz
}

// passByConfidence scores how well a frequency track matches the flat-sweep-flat
// shape of a genuine pass-by, from 0 to 1.
func passByConfidence(freqs []float64, third int) float64 {
	if third < 2 || len(freqs) < 3*third {
		return 0
	}
	before := freqs[:third]
	after := freqs[len(freqs)-third:]

	// Both plateaus should be internally steady relative to the step between
	// them. Scatter comparable to the step means the "plateaus" are just noise.
	scatter := (stdDev(before) + stdDev(after)) / 2
	step := math.Abs(mean(before) - mean(after))
	if step <= 0 {
		return 0
	}
	c := 1 - scatter/step
	if c < 0 {
		return 0
	}
	if c > 1 {
		return 1
	}
	return c
}
