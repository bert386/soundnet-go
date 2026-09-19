package acoustics_test

import (
	"math"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bert386/soundnet-go/internal/acoustics"
)

const sr = 16000

// impulseTrain synthesises n sharp decaying impulses at a fixed spacing.
func impulseTrain(n int, spacingSec, durationSec float64) []float64 {
	out := make([]float64, int(durationSec*sr))
	for k := 0; k < n; k++ {
		start := int(float64(k) * spacingSec * sr)
		for i := 0; i < int(0.02*sr) && start+i < len(out); i++ {
			t := float64(i) / sr
			out[start+i] += math.Exp(-t*300) * math.Sin(2*math.Pi*900*t)
		}
	}
	return out
}

func addNoise(x []float64, amp float64, seed int64) []float64 {
	r := rand.New(rand.NewSource(seed)) //nolint:gosec // deterministic test fixture, not security
	out := make([]float64, len(x))
	for i := range x {
		out[i] = x[i] + (r.Float64()*2-1)*amp
	}
	return out
}

// passByTone synthesises the tone heard from a source moving in a straight line
// past a microphone, from the actual physics rather than an approximation, so
// the estimator is tested against a genuine ground truth.
//
// speed in m/s, cpa in metres, f0 the emitted frequency.
func passByTone(f0, speed, cpa, durationSec float64) []float64 {
	n := int(durationSec * sr)
	out := make([]float64, n)
	t0 := durationSec / 2 // moment of closest approach
	phase := 0.0
	for i := 0; i < n; i++ {
		t := float64(i) / sr
		dt := t - t0
		// Radial velocity: positive while receding, negative while approaching.
		x := speed * dt
		radial := 0.0
		if d := math.Hypot(cpa, x); d > 0 {
			radial = speed * x / d
		}
		fObs := f0 * acoustics.SpeedOfSound / (acoustics.SpeedOfSound + radial)
		phase += 2 * math.Pi * fObs / sr
		// Amplitude falls with distance, as a real pass-by does.
		amp := cpa / math.Hypot(cpa, x)
		out[i] = amp * math.Sin(phase)
	}
	return out
}

func TestOnsetCountsDiscreteShots(t *testing.T) {
	t.Parallel()
	sig := addNoise(impulseTrain(3, 0.4, 2.0), 0.01, 1)

	got := acoustics.AnalyseOnsets(sig, sr, acoustics.DefaultOnsetConfig())
	require.NotNil(t, got)
	assert.Equal(t, 3, got.Count, "three impulses must count as three")
	assert.InDelta(t, 0.4, got.MeanIntervalSec, 0.05)
}

func TestRefractoryPeriodSuppressesEcho(t *testing.T) {
	t.Parallel()
	// One shot plus a close echo. Without a refractory period a reverberant
	// environment inflates every count, which would make shot counting useless.
	sig := impulseTrain(1, 0, 1.0)
	echoAt := int(0.03 * sr)
	for i := 0; i < int(0.02*sr) && echoAt+i < len(sig); i++ {
		tt := float64(i) / sr
		sig[echoAt+i] += 0.5 * math.Exp(-tt*300) * math.Sin(2*math.Pi*900*tt)
	}

	got := acoustics.AnalyseOnsets(addNoise(sig, 0.005, 2), sr, acoustics.DefaultOnsetConfig())
	assert.Equal(t, 1, got.Count, "an echo 30ms after the shot must not count as a second shot")
}

func TestRegularIntervalsSuggestMachine(t *testing.T) {
	t.Parallel()

	regular := acoustics.AnalyseOnsets(addNoise(impulseTrain(6, 0.25, 2.0), 0.005, 3), sr, acoustics.DefaultOnsetConfig())
	require.GreaterOrEqual(t, regular.Count, 5)
	assert.True(t, regular.Regular, "evenly spaced reports look mechanical, like a nail gun")
}

func TestSilenceProducesNoOnsets(t *testing.T) {
	t.Parallel()
	got := acoustics.AnalyseOnsets(make([]float64, sr), sr, acoustics.DefaultOnsetConfig())
	assert.Equal(t, 0, got.Count)
}

func TestSpectralTiltDistinguishesNearFromFar(t *testing.T) {
	t.Parallel()
	cfg := acoustics.DefaultLevelConfig()

	// A near source keeps its high frequencies; a distant one has lost them to
	// atmospheric absorption. Flat vs steeply falling band levels.
	near := []acoustics.Band{
		{500, -30}, {1000, -31}, {2000, -32}, {4000, -33}, {8000, -34},
	}
	far := []acoustics.Band{
		{500, -40}, {1000, -46}, {2000, -53}, {4000, -61}, {8000, -70},
	}
	samples := make([]float64, sr)

	gotNear := acoustics.AnalyseLevel(samples, near, cfg)
	gotFar := acoustics.AnalyseLevel(samples, far, cfg)

	assert.Equal(t, acoustics.ProximityNear, gotNear.Proximity)
	assert.Equal(t, acoustics.ProximityFar, gotFar.Proximity)
	assert.Less(t, gotFar.SpectralTiltDBPerDecade, gotNear.SpectralTiltDBPerDecade,
		"a more distant source must show a more negative tilt")
}

func TestTiltUnknownWithTooFewBands(t *testing.T) {
	t.Parallel()
	// A slope through two points is not a measurement. Saying "unknown" is the
	// honest result; a confident near/far call here would be fabricated.
	got := acoustics.AnalyseLevel(make([]float64, sr),
		[]acoustics.Band{{1000, -30}, {2000, -35}}, acoustics.DefaultLevelConfig())
	assert.Equal(t, acoustics.ProximityUnknown, got.Proximity)
	assert.Equal(t, 2, got.BandCount)
}

func TestAbsoluteDistanceOnlyWhenCalibrated(t *testing.T) {
	t.Parallel()
	bands := []acoustics.Band{{500, -30}, {1000, -33}, {2000, -36}, {4000, -39}}
	samples := make([]float64, sr)
	for i := range samples {
		samples[i] = 0.1 * math.Sin(2*math.Pi*440*float64(i)/sr)
	}

	uncal := acoustics.AnalyseLevel(samples, bands, acoustics.DefaultLevelConfig())
	assert.Nil(t, uncal.AbsoluteDistanceM,
		"distance in metres is meaningless without a calibrated microphone")

	cfg := acoustics.DefaultLevelConfig()
	cfg.Calibrated = true
	cfg.ReferenceSPLAt1m = 100
	cal := acoustics.AnalyseLevel(samples, bands, cfg)
	require.NotNil(t, cal.AbsoluteDistanceM)
	assert.Positive(t, *cal.AbsoluteDistanceM)
}

func TestEnvelopeSeparatesCrackFromRumble(t *testing.T) {
	t.Parallel()

	// A near thunderclap: sharp, quickly over.
	crack := make([]float64, int(1.5*sr))
	for i := range crack {
		tt := float64(i) / sr
		crack[i] = math.Exp(-tt*40) * math.Sin(2*math.Pi*200*tt)
	}
	// A distant one: the same energy spread into a long rumble.
	rumble := make([]float64, int(1.5*sr))
	for i := range rumble {
		tt := float64(i) / sr
		rumble[i] = math.Exp(-tt*2) * math.Sin(2*math.Pi*80*tt)
	}

	gotCrack := acoustics.AnalyseEnvelope(crack, sr, 0.01, 20)
	gotRumble := acoustics.AnalyseEnvelope(rumble, sr, 0.01, 20)
	require.NotNil(t, gotCrack)
	require.NotNil(t, gotRumble)

	assert.Less(t, gotCrack.DurationSec, gotRumble.DurationSec,
		"a near strike is short; a distant one rumbles, which is how duration indicates distance")
}

func TestDopplerRecoversSpeedAndCPA(t *testing.T) {
	t.Parallel()

	const trueSpeed = 20.0 // m/s, 72 km/h
	const trueCPA = 15.0   // metres
	sig := passByTone(600, trueSpeed, trueCPA, 4.0)

	got, warn := acoustics.AnalyseDoppler(sig, sr, acoustics.DefaultDopplerConfig())
	require.NotNil(t, got, "a clean synthetic pass-by must produce a fit; warning was %q", warn)

	assert.InDelta(t, trueSpeed*3.6, got.SpeedKmh, 12,
		"speed from the before/after frequency ratio")
	assert.InDelta(t, 600, got.RestFrequencyHz, 30,
		"the geometric mean of the two plateaus recovers the emitted frequency")
	assert.InDelta(t, 2.0, got.CPATimeSec, 0.5, "closest approach is mid-clip")
	assert.Positive(t, got.CPAMetres)
	assert.Greater(t, got.Confidence, 0.5)
}

func TestDopplerReportsNothingForStationarySource(t *testing.T) {
	t.Parallel()

	// A steady tone: an idling engine, not a pass-by. Inventing a speed here
	// would be indistinguishable from a real measurement downstream.
	steady := make([]float64, int(4.0*sr))
	for i := range steady {
		steady[i] = math.Sin(2 * math.Pi * 600 * float64(i) / sr)
	}

	got, warn := acoustics.AnalyseDoppler(steady, sr, acoustics.DefaultDopplerConfig())
	assert.Nil(t, got, "no shift means no speed, not a speed of zero dressed as a measurement")
	assert.NotEmpty(t, warn, "the reason must be reported, not silently swallowed")
}

func TestDopplerFasterSourceShiftsMore(t *testing.T) {
	t.Parallel()
	cfg := acoustics.DefaultDopplerConfig()

	slow, _ := acoustics.AnalyseDoppler(passByTone(600, 10, 15, 4.0), sr, cfg)
	fast, _ := acoustics.AnalyseDoppler(passByTone(600, 30, 15, 4.0), sr, cfg)
	require.NotNil(t, slow)
	require.NotNil(t, fast)

	assert.Greater(t, fast.SpeedKmh, slow.SpeedKmh,
		"a faster pass-by must yield a larger speed estimate")
}

func TestDopplerCloserPassGivesSmallerCPA(t *testing.T) {
	t.Parallel()
	cfg := acoustics.DefaultDopplerConfig()

	// A close pass sweeps abruptly; a distant one sweeps gently. That slope
	// difference is the only thing carrying distance information here.
	close, _ := acoustics.AnalyseDoppler(passByTone(600, 20, 5, 4.0), sr, cfg)
	farther, _ := acoustics.AnalyseDoppler(passByTone(600, 20, 40, 4.0), sr, cfg)
	require.NotNil(t, close)
	require.NotNil(t, farther)

	assert.Less(t, close.CPAMetres, farther.CPAMetres,
		"a closer pass must yield a smaller closest-approach estimate")
}

func TestDopplerDeclinesShortClip(t *testing.T) {
	t.Parallel()
	got, warn := acoustics.AnalyseDoppler(make([]float64, 1000), sr, acoustics.DefaultDopplerConfig())
	assert.Nil(t, got)
	assert.Contains(t, warn, "too short")
}

func TestImpulseFlagsGunshotBackfireAmbiguity(t *testing.T) {
	t.Parallel()

	// A sharp transient whose rise time lands in the region where gunshots and
	// vehicle backfires overlap. The scope requires this be flagged, not decided.
	sig := make([]float64, int(0.5*sr))
	for i := range sig {
		tt := float64(i) / sr
		sig[i] = math.Exp(-tt*200) * math.Sin(2*math.Pi*700*tt)
	}

	got := acoustics.AnalyseImpulse(sig, sr, acoustics.DefaultImpulseConfig())
	require.NotNil(t, got)
	assert.Positive(t, got.CrestFactor)
	assert.Positive(t, got.SpectralCentroidHz)
	assert.True(t, got.LowConfidence, "impulse classes overlap here and must not be asserted")
	assert.NotEmpty(t, got.Ambiguity, "the reason must be available to show the operator")
}

func TestImpulseRejectsSustainedSound(t *testing.T) {
	t.Parallel()

	// A steady tone has a low crest factor; impulse shape features say nothing
	// useful about it, and the result says so.
	steady := make([]float64, sr)
	for i := range steady {
		steady[i] = math.Sin(2 * math.Pi * 440 * float64(i) / sr)
	}

	got := acoustics.AnalyseImpulse(steady, sr, acoustics.DefaultImpulseConfig())
	require.NotNil(t, got)
	assert.Less(t, got.CrestFactor, 6.0)
	assert.True(t, got.LowConfidence)
	assert.Contains(t, got.Ambiguity, "not impulsive")
}

func TestSpectralCentroidTracksBrightness(t *testing.T) {
	t.Parallel()

	low := make([]float64, sr)
	high := make([]float64, sr)
	for i := range low {
		tt := float64(i) / sr
		low[i] = math.Sin(2 * math.Pi * 300 * tt)
		high[i] = math.Sin(2 * math.Pi * 3000 * tt)
	}

	gotLow := acoustics.AnalyseImpulse(low, sr, acoustics.DefaultImpulseConfig())
	gotHigh := acoustics.AnalyseImpulse(high, sr, acoustics.DefaultImpulseConfig())
	require.NotNil(t, gotLow)
	require.NotNil(t, gotHigh)

	assert.Less(t, gotLow.SpectralCentroidHz, gotHigh.SpectralCentroidHz)
}

// BenchmarkFullClip guards the Raspberry Pi 4 budget of under 100ms per clip.
// It runs every stage over a 3-second clip, which is the realistic worst case.
func BenchmarkFullClip(b *testing.B) {
	sig := passByTone(600, 20, 15, 3.0)
	bands := []acoustics.Band{{500, -40}, {1000, -46}, {2000, -53}, {4000, -61}, {8000, -70}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = acoustics.AnalyseLevel(sig, bands, acoustics.DefaultLevelConfig())
		_ = acoustics.AnalyseOnsets(sig, sr, acoustics.DefaultOnsetConfig())
		_ = acoustics.AnalyseEnvelope(sig, sr, 0.01, 20)
		_, _ = acoustics.AnalyseDoppler(sig, sr, acoustics.DefaultDopplerConfig())
		_ = acoustics.AnalyseImpulse(sig, sr, acoustics.DefaultImpulseConfig())
	}
}
