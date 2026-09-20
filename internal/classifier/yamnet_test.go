package classifier

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bert386/soundnet-go/internal/conf"
	"github.com/bert386/soundnet-go/internal/datastore/v2/entities"
	"github.com/bert386/soundnet-go/internal/detection"

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

	silence := [][]float32{make([]float32, yamnetClipSeconds*yamnetSampleRate)}

	// Silence classifies confidently as Silence, which is precisely why it must
	// produce no detection: it is a correct answer about nothing happening.
	results, err := y.Predict(t.Context(), silence)
	require.NoError(t, err)
	assert.Empty(t, results,
		"Silence is not in the event taxonomy, so a silent clip must yield no detections")

	// The end-to-end path - framing, inference, aggregation, labelling - is still
	// worth proving, so run the same clip with the taxonomy filter opened up.
	// This is the assertion that would catch a broken framing or a second
	// activation being applied to the already-sigmoid output.
	for i := range y.emit {
		y.emit[i] = true
	}
	results, err = y.Predict(t.Context(), silence)
	require.NoError(t, err)
	require.NotEmpty(t, results)

	assert.Equal(t, "silence", results[0].Species,
		"silence should score the Silence class highest")
	// A probability, not a logit. A second sigmoid would collapse this toward
	// 0.5 and fail the bound.
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

// TestYAMNetResolvesAGalleryInstall proves the last link in the chain: that the
// catalog entry's roles and filenames are what resolveFamilyPaths actually looks
// for on disk.
//
// Everything else can be right - registry entry, loader, adapter - and the model
// still never loads if the catalog declares a role the resolver does not read or
// a filename the installer does not write. Rather than reason about that, this
// lays out a gallery install and resolves against it.
func TestYAMNetResolvesAGalleryInstall(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("yamnet-v1")
	require.True(t, ok)
	require.Equal(t, RegistryIDYAMNet, entry.RegistryID,
		"resolveInstalledPaths matches catalog entries on RegistryID")

	modelsDir := t.TempDir()
	subdir := filepath.Join(modelsDir, entry.ID)
	require.NoError(t, os.MkdirAll(subdir, 0o755))

	var wantModel, wantLabels string
	for _, f := range entry.Files {
		require.NotEmptyf(t, f.LocalName, "file %q must declare a local name", f.RemotePath)
		p := filepath.Join(subdir, f.LocalName)
		require.NoError(t, os.WriteFile(p, []byte("placeholder"), 0o600))
		switch f.Role {
		case RoleModel:
			wantModel = p
		case RoleLabels:
			wantLabels = p
		}
	}
	require.NotEmpty(t, wantModel, "the entry must declare a model-role file")
	require.NotEmpty(t, wantLabels, "the entry must declare a labels-role file, or the classes cannot be named")

	o := &Orchestrator{modelsDir: modelsDir}
	res := o.resolveFamilyPaths(RegistryIDYAMNet, modelFileSet{}, false)
	assert.Equal(t, wantModel, res.resolved.model)
	assert.Equal(t, wantLabels, res.resolved.labels)

	// And the loader gets far enough to try loading them: the placeholder bytes
	// are not a TFLite model, so this must fail on the model, not on resolution.
	_, _, err := o.buildYAMNet(&conf.Settings{}, 1)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "not installed",
		"paths resolved, so the failure must come from the model file rather than from resolution")
}

// TestYAMNetBuildSaysSoWhenNotInstalled keeps the empty-models-dir case honest:
// the operator needs to be told to install it, not handed a parse error.
func TestYAMNetBuildSaysSoWhenNotInstalled(t *testing.T) {
	t.Parallel()
	o := &Orchestrator{modelsDir: t.TempDir()}
	_, _, err := o.buildYAMNet(&conf.Settings{}, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not installed")
}

// TestOnlyTaxonomyClassesAreReported guards the fix for a real production
// failure: the first live run emitted every class above the score floor, so
// "animal" at 0.96, "bird" at 0.92, "whistling" at 0.99 and "insect" at 1.00
// were stored as detections - 49 rows in six minutes, burying the birds and the
// events SoundNet exists to find.
//
// Indices below are read off the committed class map, not recalled.
func TestOnlyTaxonomyClassesAreReported(t *testing.T) {
	t.Parallel()
	emit := reportable(yamnetClasses)

	count := 0
	for _, ok := range emit {
		if ok {
			count++
		}
	}
	assert.Positive(t, count, "some classes must be reportable, or YAMNet can never detect anything")
	assert.Less(t, count, 100, "only the taxonomy's own classes should be reported, not most of AudioSet")

	// Classes SoundNet exists to find.
	for name, idx := range map[string]int{
		"Jet engine": 331, "Helicopter": 333, "Civil defense siren": 391, "Machine gun": 422,
	} {
		assert.Truef(t, emit[idx], "%s (index %d) is a default-enabled taxonomy class and must be reported", name, idx)
	}

	// The exact classes that flooded the live run. Each is a real AudioSet class
	// and a perfectly good classification; none is an event worth a detection row.
	for name, idx := range map[string]int{
		"Speech": 0, "Whistling": 35, "Animal": 67, "Bird": 106,
		"Insect": 121, "Mosquito": 123, "Silence": 494,
	} {
		assert.Falsef(t, emit[idx], "%s (index %d) is not in the event taxonomy and must not be reported", name, idx)
	}
}

// TestReportableIsBoundsSafe covers a class map that disagrees with the
// taxonomy. An index past the end of the model's output would panic on the
// write, which is a bad way to find out the artefact changed.
func TestReportableIsBoundsSafe(t *testing.T) {
	t.Parallel()
	assert.Len(t, reportable(10), 10, "a short class list must not panic or over-allocate")
	assert.Empty(t, reportable(0), "zero classes yields no reportable classes")
}

// TestYAMNetIsNotFiledAsABird pins the model-type resolution.
//
// detection.ResolveModelType falls through to ModelTypeBird by default, and
// taxonomicClassForModel then assigns the Aves class - so before this, every
// YAMNet detection was stored as a bird. Observed in production: insects,
// whistling and generic animal classes all carrying modelType "bird".
func TestYAMNetIsNotFiledAsABird(t *testing.T) {
	t.Parallel()
	info := ModelRegistry[RegistryIDYAMNet]
	got := detection.ResolveModelType(info.DetectionName, info.DetectionVersion)

	assert.Equal(t, entities.ModelTypeMulti, got,
		"YAMNet classifies acoustic events, not taxa; Multi is the no-default-taxonomic-class case")
	assert.NotEqual(t, entities.ModelTypeBird, got,
		"the bird default would store every acoustic event with the Aves taxonomic class")
}
