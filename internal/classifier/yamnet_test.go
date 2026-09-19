package classifier

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SoundNet: tests for the YAMNet adapter.
//
// Internal tests, because most of what is worth pinning here is unexported: the
// frame tiling, the class-map loader, and the registry spec. Inference itself
// needs the 4 MB model file, which is not committed; the one test that runs it
// skips unless the file is present (see TestYAMNetInference).

const yamnetFixtureClassMap = "../eventclass/testdata/yamnet_class_map.csv"

func TestNormaliseAudioSetLabel(t *testing.T) {
	t.Parallel()
	// The form has to match internal/labels/nonbird's keys exactly. A label that
	// misses takes the species branch in the datastore and is stored as a bird.
	for display, want := range map[string]string{
		"Jet engine":                   "jet_engine",
		"Child speech, kid speaking":   "child_speech_and_kid_speaking",
		"Propeller, airscrew":          "propeller_and_airscrew",
		"Bathtub (filling or washing)": "bathtub_(filling_or_washing)",
		"Accelerating, revving, vroom": "accelerating_and_revving_and_vroom",
		"Speech":                       "speech",
	} {
		assert.Equal(t, want, NormaliseAudioSetLabel(display), "normalising %q", display)
	}
}

func TestLoadYAMNetClassMap(t *testing.T) {
	t.Parallel()
	labels, err := loadYAMNetClassMap(yamnetFixtureClassMap)
	require.NoError(t, err)

	require.Len(t, labels, yamnetClasses, "YAMNet publishes 521 AudioSet classes")
	// Spot-checked against the published map. These indices are the join between
	// model output and the event taxonomy, so they are worth stating literally.
	assert.Equal(t, "speech", labels[0])
	assert.Equal(t, "jet_engine", labels[331])
	assert.Equal(t, "helicopter", labels[333])
	assert.Equal(t, "civil_defense_siren", labels[391])

	for i, l := range labels {
		require.NotEmptyf(t, l, "class %d has an empty label", i)
	}
}

func TestLoadYAMNetClassMapRejectsBadFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
		return p
	}

	// A gap means the model's index N would be labelled with class N+1's name and
	// every detection past the gap would be wrong, so refuse rather than guess.
	_, err := loadYAMNetClassMap(write("gap.csv", "index,mid,display_name\n0,/m/a,Speech\n2,/m/c,Music\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "index 1")

	_, err = loadYAMNetClassMap(write("dupe.csv", "index,mid,display_name\n0,/m/a,Speech\n0,/m/b,Music\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "repeats index")

	_, err = loadYAMNetClassMap(write("empty.csv", "index,mid,display_name\n"))
	require.Error(t, err)

	_, err = loadYAMNetClassMap(filepath.Join(dir, "absent.csv"))
	require.Error(t, err)
}

// TestLoadYAMNetClassMapTrustsTheIndexColumn proves row order is not assumed.
func TestLoadYAMNetClassMapTrustsTheIndexColumn(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "shuffled.csv")
	require.NoError(t, os.WriteFile(p, []byte(
		"index,mid,display_name\n2,/m/c,Helicopter\n0,/m/a,Speech\n1,/m/b,Jet engine\n"), 0o600))

	labels, err := loadYAMNetClassMap(p)
	require.NoError(t, err)
	assert.Equal(t, []string{"speech", "jet_engine", "helicopter"}, labels,
		"labels must be ordered by the index column, not by row position")
}

func TestYAMNetFrameOffsets(t *testing.T) {
	t.Parallel()

	// The real case: four frames tiling a 3 s window at 16 kHz.
	offsets := yamnetFrameOffsets(yamnetClipSeconds*yamnetSampleRate, yamnetFrameSamples, yamnetFramesPerClip)
	assert.Equal(t, []int{0, 10800, 21600, 32400}, offsets)

	// The properties that matter, stated rather than implied by the literal:
	// nothing reads past the end, and the last frame reaches it exactly, so no
	// part of the window goes unclassified.
	clip := yamnetClipSeconds * yamnetSampleRate
	for _, o := range offsets {
		assert.LessOrEqual(t, o+yamnetFrameSamples, clip, "frame at %d reads past the clip", o)
	}
	assert.Equal(t, clip, offsets[len(offsets)-1]+yamnetFrameSamples, "the last frame must end at the clip end")

	// Degenerate inputs must not panic or produce an out-of-range read.
	assert.Nil(t, yamnetFrameOffsets(100, 15600, 4), "a clip shorter than one frame yields no frames")
	assert.Equal(t, []int{0}, yamnetFrameOffsets(15600, 15600, 4), "an exactly-one-frame clip yields one frame")
	assert.Equal(t, []int{0}, yamnetFrameOffsets(48000, 15600, 1))
	assert.Nil(t, yamnetFrameOffsets(48000, 15600, 0))
}

// TestYAMNetSpecDoesNotTruncate is the guard for the trap that would otherwise
// make YAMNet load, report healthy, and never infer once.
//
// ModelSpec.ClipSizeBytes computes SampleRate * int(ClipLength.Seconds()), and
// int() truncates: a 0.975 s clip yields zero bytes, so the analysis buffer is
// zero-length and the monitor's read never matches.
func TestYAMNetSpecDoesNotTruncate(t *testing.T) {
	t.Parallel()
	spec := ModelRegistry[RegistryIDYAMNet].Spec

	assert.Equal(t, yamnetSampleRate, spec.SampleRate)
	assert.Equal(t, yamnetClipSeconds*time.Second, spec.ClipLength)
	assert.Equal(t, 16000, spec.EffectiveSampleRate(),
		"the buffer consumer resamples to this; 48 kHz capture must be downsampled")

	assert.Positive(t, spec.ClipSizeBytes(), "a zero-size clip yields a buffer that never fires")
	// 16000 samples/s * 3 s * 1 channel * 2 bytes.
	assert.Equal(t, 96000, spec.ClipSizeBytes())

	// And the window must hold a whole number of model frames.
	samples := spec.ClipSizeBytes() / 2
	assert.GreaterOrEqual(t, samples, yamnetFrameSamples,
		"the analysis window must be at least one model frame long")
}

// TestNoRegisteredModelHasATruncatingClip generalises the guard above. Any model
// whose ClipLength is not a whole number of seconds silently gets a short or
// zero-length buffer.
func TestNoRegisteredModelHasATruncatingClip(t *testing.T) {
	t.Parallel()
	for id, info := range ModelRegistry {
		spec := info.Spec
		if spec.ClipLength == 0 {
			continue
		}
		assert.Zerof(t, spec.ClipLength%time.Second,
			"model %q has a %s clip; ClipSizeBytes truncates to whole seconds and would under-size its buffer",
			id, spec.ClipLength)
		assert.Positivef(t, spec.ClipSizeBytes(), "model %q computes a zero-size analysis clip", id)
	}
}

func TestYAMNetLoaderIsRegistered(t *testing.T) {
	t.Parallel()
	// Without this entry LoadModel returns "loader not yet implemented" and
	// loadEnabledModels skips with a warning - the model appears configured and
	// simply never runs.
	_, ok := modelLoaders[RegistryIDYAMNet]
	assert.True(t, ok, "YAMNet must have a loader registered in modelLoaders")
}

func TestNewYAMNetRejectsIncompleteConfig(t *testing.T) {
	t.Parallel()
	_, err := NewYAMNet(nil)
	require.Error(t, err)

	_, err = NewYAMNet(&YAMNetConfig{ModelPath: "x.tflite"})
	require.Error(t, err, "a model without a class map cannot be labelled")

	_, err = NewYAMNet(&YAMNetConfig{LabelPath: yamnetFixtureClassMap})
	require.Error(t, err, "a class map without a model is not a classifier")
}

// TestYAMNetInference runs the real model when it is available.
//
// Skipped by default: the artefact is 4 MB and is not committed, and tests must
// not reach the network. Point SOUNDNET_YAMNET_MODEL at a local copy to run it.
// Everything above this line is exercised unconditionally.
func TestYAMNetInference(t *testing.T) {
	t.Parallel()
	modelPath := os.Getenv("SOUNDNET_YAMNET_MODEL")
	if modelPath == "" {
		t.Skip("set SOUNDNET_YAMNET_MODEL to a yamnet.tflite to run inference tests")
	}
	if _, err := os.Stat(modelPath); err != nil {
		t.Skipf("SOUNDNET_YAMNET_MODEL is set but unreadable: %v", err)
	}

	y, err := NewYAMNet(&YAMNetConfig{ModelPath: modelPath, LabelPath: yamnetFixtureClassMap, Threads: 1})
	require.NoError(t, err)
	t.Cleanup(func() { _ = y.Close() })

	require.Equal(t, yamnetClasses, y.NumSpecies())

	// Silence is the one input whose correct answer is known without a corpus,
	// and it exercises the whole path: framing, inference, aggregation, labelling.
	silence := [][]float32{make([]float32, yamnetClipSeconds*yamnetSampleRate)}
	results, err := y.Predict(t.Context(), silence)
	require.NoError(t, err)
	require.NotEmpty(t, results)

	assert.Equal(t, "silence", results[0].Species,
		"silence should score the Silence class highest")
	// Already-sigmoid output: a probability, not a logit. If a second activation
	// were ever applied this would collapse toward 0.5 and this bound would fail.
	assert.Greater(t, results[0].Confidence, float32(0.5))
	assert.LessOrEqual(t, results[0].Confidence, float32(1.0))

	for _, r := range results {
		assert.GreaterOrEqual(t, r.Confidence, float32(yamnetScoreFloor))
		assert.NotEmpty(t, r.Species)
	}
}

func TestYAMNetPredictRejectsUnusableAudio(t *testing.T) {
	t.Parallel()
	// No model needed: these rejections happen before the classifier is touched.
	y := &YAMNet{labels: make([]string, yamnetClasses), scoreBuf: make([]float32, yamnetClasses)}

	_, err := y.Predict(t.Context(), nil)
	require.Error(t, err)

	_, err = y.Predict(t.Context(), [][]float32{{}})
	require.Error(t, err)

	// Shorter than one frame: padding it would feed the model silence it never
	// heard and label the result as if it had.
	_, err = y.Predict(t.Context(), [][]float32{make([]float32, yamnetFrameSamples-1)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least")

	// Long enough to frame, but the classifier is nil (as after Close).
	_, err = y.Predict(t.Context(), [][]float32{make([]float32, yamnetFrameSamples)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not initialized")
}
