package classifier

import (
	"math"
	"testing"

	"github.com/bert386/soundnet-go/internal/eventclass"
)

// rms of a signal, for comparing what the filter kept against what it removed.
func rms(x []float32) float64 {
	var total float64
	for _, v := range x {
		total += float64(v) * float64(v)
	}
	return math.Sqrt(total / float64(len(x)))
}

func tone(freqHz float64, n int, sampleRate float64) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = float32(math.Sin(2 * math.Pi * freqHz * float64(i) / sampleRate))
	}
	return out
}

// TestLowPassKeepsAircraftAndRemovesBirds is the whole premise of the second
// pass stated as a measurement: an aircraft's fundamental survives the filter
// and the birdsong masking it does not.
//
// 250 Hz and 4 kHz are the real numbers from the labelled clips - 40-55% of an
// aircraft clip's energy is below 250 Hz, and the birds that bury it sit well
// above 1.5 kHz.
func TestLowPassKeepsAircraftAndRemovesBirds(t *testing.T) {
	t.Parallel()
	const n = 16000

	cases := []struct {
		name        string
		freqHz      float64
		wantAtLeast float64 // fraction of the input amplitude that must survive
		wantAtMost  float64
	}{
		{"aircraft fundamental at 250 Hz", 250, 0.85, 1.05},
		{"below the cutoff at 800 Hz", 800, 0.50, 1.05},
		{"birdsong at 4 kHz", 4000, 0, 0.02},
		{"birdsong at 6 kHz", 6000, 0, 0.005},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := tone(tc.freqHz, n, yamnetSampleRate)
			out := make([]float32, n)
			lowPassInto(out, in, yamnetSampleRate)

			// Skip the filter's settling transient before measuring.
			ratio := rms(out[n/4:]) / rms(in[n/4:])
			if ratio < tc.wantAtLeast || ratio > tc.wantAtMost {
				t.Errorf("%.0f Hz survived at %.3f of input, want between %.3f and %.3f",
					tc.freqHz, ratio, tc.wantAtLeast, tc.wantAtMost)
			}
		})
	}
}

// TestLowPassDoesNotCarryStateBetweenWindows guards the mistake a reusable
// filter invites: leaving the previous window's tail in the delay line, which
// would make a detection depend on the window before it.
func TestLowPassDoesNotCarryStateBetweenWindows(t *testing.T) {
	t.Parallel()
	const n = 4000
	loud := tone(200, n, yamnetSampleRate)
	quiet := make([]float32, n)

	first := make([]float32, n)
	lowPassInto(first, loud, yamnetSampleRate)

	after := make([]float32, n)
	lowPassInto(after, quiet, yamnetSampleRate)

	if got := rms(after); got > 1e-9 {
		t.Errorf("silence after a loud window produced rms %g; filter state leaked", got)
	}
}

// TestAircraftClassesCoverTheDomainAndNothingElse pins the scope of the second
// pass. Widening it silently is the failure this test exists to catch: the
// low-pass raises the aircraft score of clips with no aircraft in them too, so
// the margin only holds for the classes it was measured on.
func TestAircraftClassesCoverTheDomainAndNothingElse(t *testing.T) {
	t.Parallel()
	const numClasses = 521
	mask := aircraftClasses(numClasses)

	marked := 0
	for i, on := range mask {
		if !on {
			continue
		}
		marked++
		// Every marked index must belong to the aircraft domain.
		found := false
		for _, c := range eventclass.InDomain(eventclass.DomainAircraft) {
			if c.AudioSetIndex == i {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("index %d is marked but is not an aircraft class", i)
		}
	}
	if marked == 0 {
		t.Fatal("no aircraft classes marked; the second pass would be a no-op")
	}

	// And nothing from a neighbouring domain leaked in. Vehicle is the one that
	// matters: it is the class an aircraft most often lands in, and raising it
	// on a low-passed window would also raise it on rustling grass.
	for _, c := range eventclass.InDomain(eventclass.DomainVehicle) {
		if c.AudioSetIndex >= 0 && c.AudioSetIndex < numClasses && mask[c.AudioSetIndex] {
			t.Errorf("vehicle class %q (index %d) is marked; the measurement does not cover it",
				c.Label, c.AudioSetIndex)
		}
	}
}

// fixedClassifier returns one fixed score vector for every frame.
//
// These tests drive mergeLowPassPass directly rather than going through
// Predict, so every call this sees is part of the low-passed pass; scoreBuf is
// seeded with the raw scores by the test instead. An earlier version of this
// fake counted frames and returned the raw vector for the first four, modelling
// a sequence that does not happen here - and the test failed for that reason
// rather than for anything wrong with the code.
type fixedClassifier struct {
	lowPassed []float32
	calls     int
}

func (f *fixedClassifier) Predict(samples []float32) ([]float32, error) {
	f.calls++
	cp := make([]float32, len(f.lowPassed))
	copy(cp, f.lowPassed)
	return cp, nil
}

func (f *fixedClassifier) NumSpecies() int { return len(f.lowPassed) }
func (f *fixedClassifier) Close()          {}

// TestSecondPassRaisesOnlyAircraftClasses is the behavioural test. A
// low-passed window that scores higher on everything must lift the aircraft
// classes and leave every other class at its raw value.
func TestSecondPassRaisesOnlyAircraftClasses(t *testing.T) {
	t.Parallel()

	const numClasses = 521
	mask := aircraftClasses(numClasses)
	var anAircraftIndex = -1
	for i, on := range mask {
		if on {
			anAircraftIndex = i
			break
		}
	}
	if anAircraftIndex < 0 {
		t.Fatal("no aircraft class to test with")
	}

	raw := make([]float32, numClasses)
	lowPassed := make([]float32, numClasses)
	for i := range raw {
		raw[i] = 0.10
		lowPassed[i] = 0.90 // higher everywhere, so any leak is visible
	}

	y := &YAMNet{
		classifier: &fixedClassifier{lowPassed: lowPassed},
		scoreBuf:   make([]float32, numClasses),
		frameBuf:   make([]float32, yamnetFrameSamples),
		aircraft:   mask,
	}
	copy(y.scoreBuf, raw)

	if err := y.mergeLowPassPass(make([]float32, yamnetSampleRate*yamnetClipSeconds)); err != nil {
		t.Fatalf("mergeLowPassPass: %v", err)
	}

	if got := y.scoreBuf[anAircraftIndex]; got != 0.90 {
		t.Errorf("aircraft class %d = %.2f, want 0.90 from the low-passed pass", anAircraftIndex, got)
	}
	for i, got := range y.scoreBuf {
		if mask[i] {
			continue
		}
		if got != 0.10 {
			t.Errorf("non-aircraft class %d = %.2f, want the raw 0.10 left untouched", i, got)
			break
		}
	}
}

// TestSecondPassNeverLowersAScore checks the combination rule. Taking the
// maximum means an aircraft that was already clear cannot be penalised for the
// filter having removed its higher harmonics.
func TestSecondPassNeverLowersAScore(t *testing.T) {
	t.Parallel()

	const numClasses = 521
	mask := aircraftClasses(numClasses)
	raw := make([]float32, numClasses)
	lowPassed := make([]float32, numClasses)
	for i := range raw {
		raw[i] = 0.80
		lowPassed[i] = 0.05 // the filter destroyed the signal
	}

	y := &YAMNet{
		classifier: &fixedClassifier{lowPassed: lowPassed},
		scoreBuf:   make([]float32, numClasses),
		frameBuf:   make([]float32, yamnetFrameSamples),
		aircraft:   mask,
	}
	copy(y.scoreBuf, raw)

	if err := y.mergeLowPassPass(make([]float32, yamnetSampleRate*yamnetClipSeconds)); err != nil {
		t.Fatalf("mergeLowPassPass: %v", err)
	}
	for i, got := range y.scoreBuf {
		if got != 0.80 {
			t.Fatalf("class %d dropped to %.2f; the pass must only ever raise", i, got)
		}
	}
}
