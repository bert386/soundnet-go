package classifier

// SOUNDNET: the CED-tiny adapter.
//
// CED is a 2023 audio tagger distilled from transformer ensembles, and on this
// station it is markedly better than YAMNet at the one distinction that matters
// most here. Given the same clips, YAMNet called a large truck and a propeller
// aircraft both "Vehicle 0.80"; CED gives the truck an aircraft score of 0.000
// and the aeroplane 0.374. It also called a person whistling "Whistling" where
// YAMNet called it a police siren at 0.74. See doc/soundnet/MODEL_EVAL.md.
//
// Three things differ from the YAMNet adapter and are worth stating up front.
//
//  1. **The mel front-end is inside the graph.** The stock CED export takes a
//     log-mel tensor and leaves the front-end to the caller, which is why
//     sherpa-onnx reimplements it in C++. This fork re-exported it with
//     torchaudio's MelSpectrogram fused in, matching what every other model
//     here does and what CED was actually trained with - the alternative was
//     matching eight feature parameters by inference in Go, where a wrong one
//     produces a model that loads, runs and returns plausible nonsense.
//
//  2. **One inference per window, not four.** YAMNet consumes 0.975 s and has
//     to tile a 3 s window; CED takes the whole window at once.
//
//  3. **527 classes, not 521, in AudioSet's own order.** YAMNet drops six and
//     uses its own ordering, so the AudioSetIndex column in the event taxonomy
//     - which is pinned to YAMNet - does not apply. The join here is the label
//     text, which both sides hold.

import (
	"context"
	"sync"
	"time"

	"github.com/bert386/soundnet-go/internal/datastore"
	"github.com/bert386/soundnet-go/internal/errors"
	"github.com/bert386/soundnet-go/internal/eventclass"
	"github.com/bert386/soundnet-go/internal/inference"
	"github.com/bert386/soundnet-go/internal/labels/vocalization"
)

// CEDConfig configures a CED instance.
type CEDConfig struct {
	ModelPath       string // path to the fused ced_tiny ONNX
	LabelPath       string // path to class_labels_indices.csv
	ONNXRuntimePath string // ORT shared library, from BirdNET settings
	Threads         int    // 0 lets ONNX Runtime choose
}

// CED is a loaded CED classifier. Not goroutine-safe by itself; the Orchestrator
// serialises inference across all models and the mutex guards against a
// concurrent Close.
type CED struct {
	mu         sync.Mutex
	classifier inference.Classifier
	labels     []string
	modelPath  string
	info       ModelInfo

	// emit[i] reports whether class i is one SoundNet records. See
	// reportableByLabel.
	emit []bool
}

// reportableByLabel marks which classes are worth emitting, matched on label
// text rather than on index.
//
// The YAMNet adapter matches on AudioSetIndex because the taxonomy documents
// that index as authoritative for YAMNet's 521-class map. CED has 527 classes in
// AudioSet's own ordering, so those indices are simply a different numbering of
// an overlapping set and using them here would mislabel every detection while
// failing nothing. Both models emit AudioSet display names, normalised
// identically, so the text is the join that holds across both.
func reportableByLabel(labels []string) []bool {
	emit := make([]bool, len(labels))
	for i, label := range labels {
		// The deliberate exception, identical to YAMNet's: the privacy and
		// dog-bark filters can only act on results the adapter hands them, so a
		// class withheld here is protection silently removed. They are dropped
		// before storage rather than before the filters - see
		// internal/analysis/processor/filteronly_soundnet.go.
		if vocalization.IsHuman(label) || vocalization.IsDog(label) {
			emit[i] = true
			continue
		}
		if class, found := eventclass.Resolve(label); found && class.DefaultEnabled {
			emit[i] = true
		}
	}
	return emit
}

// NewCED loads the model and its class map.
func NewCED(cfg *CEDConfig) (*CED, error) {
	labels, err := loadAudioSetClassMap(cfg.LabelPath, "classifier.ced")
	if err != nil {
		return nil, err
	}
	if len(labels) != cedClasses {
		return nil, errors.Newf("CED class map has %d classes, want %d", len(labels), cedClasses).
			Component("classifier.ced").
			Category(errors.CategoryValidation).
			Context("labels_path", cfg.LabelPath).
			Build()
	}

	if err := inference.InitONNXRuntime(cfg.ONNXRuntimePath); err != nil {
		return nil, errors.New(err).
			Component("classifier.ced").
			Category(errors.CategoryModelInit).
			Context("model", RegistryIDCED).
			Build()
	}

	classifier, err := inference.NewONNXClassifier(cfg.ModelPath, inference.ONNXClassifierOptions{
		Labels:  labels,
		Threads: cfg.Threads,
	})
	if err != nil {
		return nil, errors.New(err).
			Component("classifier.ced").
			Category(errors.CategoryModelInit).
			Context("model_path", cfg.ModelPath).
			Context("label_count", len(labels)).
			Build()
	}

	info := ModelRegistry[RegistryIDCED]
	return &CED{
		classifier: classifier,
		labels:     labels,
		modelPath:  cfg.ModelPath,
		info:       info,
		emit:       reportableByLabel(labels),
	}, nil
}

// Predict runs CED over one analysis window.
//
// One inference, unlike YAMNet's four: the fused graph takes the whole 3 s
// window. The window length is fixed by construction - CED interpolates its
// positional embeddings from the input length and the export baked that grid in,
// so a differently sized window fails loudly rather than returning nonsense,
// which is the right way round.
func (c *CED) Predict(ctx context.Context, samples [][]float32) ([]datastore.Results, error) {
	span, _ := startPredictSpan(ctx, RegistryIDCED, samples)
	defer span.Finish()

	start := time.Now()

	if len(samples) == 0 || len(samples[0]) == 0 {
		span.markErrored(errTypeEmptySample)
		return nil, errors.Newf("empty audio sample").
			Component("classifier.ced").
			Category(errors.CategoryValidation).
			Build()
	}
	clip := samples[0]
	if len(clip) != cedWindowSamples {
		span.markErrored(errTypeEmptySample)
		return nil, errors.Newf("CED needs exactly %d samples, got %d", cedWindowSamples, len(clip)).
			Component("classifier.ced").
			Category(errors.CategoryValidation).
			Build()
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.classifier == nil {
		span.markErrored(errTypeClassifierNil)
		return nil, errors.Newf("CED classifier is not initialized").
			Component("classifier.ced").
			Category(errors.CategoryModelInit).
			Build()
	}

	scores, err := c.classifier.Predict(clip)
	if err != nil {
		err = errors.New(err).
			Component("classifier.ced").
			Category(errors.CategoryAudio).
			Context("model", RegistryIDCED).
			Build()
		recordPredictionFailure(span, RegistryIDCED, errTypeInvokeFailed, start, err)
		return nil, err
	}
	if idx := firstNonFinite(scores); idx != noNonFiniteScore {
		// A NaN compares false against every threshold, so it survives as a
		// detection for whichever label sorts first rather than being dropped.
		err = newNonFiniteScoreError(nonFiniteScore{modelID: RegistryIDCED, index: idx, count: len(scores)}, c.RuntimeInfo)
		recordPredictionFailure(span, RegistryIDCED, errTypeNonFiniteLogits, start, err)
		return nil, err
	}
	if len(scores) != len(c.labels) {
		err := errors.Newf("CED emitted %d scores, expected %d", len(scores), len(c.labels)).
			Component("classifier.ced").
			Category(errors.CategoryModelInit).
			Build()
		recordPredictionFailure(span, RegistryIDCED, errTypeLabelMismatch, start, err)
		return nil, err
	}

	// No activation applied. CED's output is already per-class probability -
	// verified against the reference implementation, whose printed "prob" values
	// match these to float32 noise. Applying a sigmoid here would compress
	// everything into [0.5, 0.73], exactly as it would for YAMNet.
	results := make([]datastore.Results, 0, defaultTopKResults*2)
	for i, s := range scores {
		if s < cedScoreFloor || !c.emit[i] {
			continue
		}
		results = append(results, datastore.Results{Species: c.labels[i], Confidence: s})
	}

	topResults := getTopKResults(results, defaultTopKResults)
	recordPredictionSuccess(span, len(topResults), start)
	return topResults, nil
}

// Spec returns the audio framing CED expects.
func (c *CED) Spec() ModelSpec { return c.info.Spec }

// ModelID returns the registry ID.
func (c *CED) ModelID() string { return RegistryIDCED }

// ModelName returns the display name.
func (c *CED) ModelName() string { return c.info.Name }

// ModelVersion returns the detection version.
func (c *CED) ModelVersion() string { return c.info.DetectionVersion }

// NumSpecies returns the class count.
func (c *CED) NumSpecies() int { return len(c.labels) }

// Labels returns a copy of the class labels.
func (c *CED) Labels() []string {
	out := make([]string, len(c.labels))
	copy(out, c.labels)
	return out
}

// RuntimeInfo describes where and how CED is running.
func (c *CED) RuntimeInfo() (device, backend, precision string) {
	return deviceCPU, BackendONNX, string(QuantizationFP32)
}

// ResolvedModelPath returns the model file actually loaded.
func (c *CED) ResolvedModelPath() string { return c.modelPath }

// Close releases the runtime session.
func (c *CED) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.classifier != nil {
		c.classifier.Close()
		c.classifier = nil
	}
	return nil
}
