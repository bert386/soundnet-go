package classifier

import (
	"context"
	"encoding/csv"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bert386/soundnet-go/internal/datastore"
	"github.com/bert386/soundnet-go/internal/errors"
	"github.com/bert386/soundnet-go/internal/eventclass"
	"github.com/bert386/soundnet-go/internal/inference"
	"github.com/bert386/soundnet-go/internal/inference/tflite"
	"github.com/bert386/soundnet-go/internal/labels/vocalization"
	"github.com/bert386/soundnet-go/internal/logger"
)

// SoundNet: YAMNet, the acoustic event classifier.
//
// This is the piece that turns a bird detector into a general event detector.
// Everything else in the fork - the taxonomy, the DSP diagnostics, ADS-B
// enrichment - is downstream of something non-bird being classified in the
// first place, and until this existed nothing ever was.
//
// The model's real shape, read off the artefact rather than assumed:
//
//	in [0] "waveform_binary"  float32 [15600]   (0.975 s of 16 kHz mono)
//	out[0] final_output       float32 [1 521]   (AudioSet scores)
//
// Two consequences worth stating, because both contradict what the scope and
// the upstream docs would lead you to expect:
//
//  1. There is no embeddings output. The published YAMNet graph has three
//     outputs (scores, 1024-d embeddings, log-mel patches); this MediaPipe
//     build exposes only the scores. M4's sub-classification heads were meant
//     to train on those embeddings, so M4 needs either a different artefact or
//     a different feature source. See doc/soundnet/OPEN_DECISIONS.md.
//
//  2. The scores are already probabilities. Verified empirically: silence
//     scores Silence at 0.80, a 440 Hz tone scores Sine wave at 0.996, and the
//     vector sums above 1.0, so it is per-class sigmoid rather than softmax.
//     BirdNET applies a sigmoid and Perch a softmax to their backends' raw
//     logits; applying either here would be wrong. A second sigmoid in
//     particular would squash everything into [0.5, 0.73] and make every class
//     look like a half-confident detection.

const (
	// yamnetFrameSamples is fixed by the model: the input tensor is exactly this
	// long, and the TFLite wrapper rejects any other length rather than padding.
	yamnetFrameSamples = 15600

	// yamnetClipSeconds is the analysis window SoundNet feeds YAMNet, which is
	// deliberately NOT the model's own 0.975 s frame.
	//
	// Three seconds matches BirdNET's window, which matters more than it looks:
	// the diagnostics engine measures rise time, spectral tilt and Doppler over
	// the retained clip, and Doppler needs seconds of pass-by to be recoverable
	// at all. Sharing one window means a YAMNet detection and its diagnostics
	// describe the same audio.
	//
	// It also avoids a trap. ModelSpec.ClipSizeBytes computes
	// SampleRate * int(ClipLength.Seconds()), and int(0.975) is 0 - so a
	// sub-second spec yields a zero-byte analysis buffer, a read size of zero,
	// and a monitor that never fires. The model would load, report healthy, and
	// never infer once.
	yamnetClipSeconds = 3

	// yamnetFramesPerClip is how many model frames tile one analysis window.
	// Four 15600-sample frames spaced evenly across 48000 samples cover it
	// exactly, with a hop of 10800 (~0.675 s) and ~31% overlap between frames.
	// An event landing on a frame boundary is therefore still seen whole by a
	// neighbouring frame.
	yamnetFramesPerClip = 4

	// yamnetScoreFloor drops classes that scored essentially nothing before the
	// top-K sort. It is not a detection threshold - that is the user's, applied
	// downstream - only a guard against returning 500-odd rows of noise per
	// window, most of them at the model's 1/256 quantisation step.
	yamnetScoreFloor = 0.01
)

// YAMNetConfig configures a YAMNet instance.
type YAMNetConfig struct {
	ModelPath  string // path to yamnet.tflite
	LabelPath  string // path to yamnet_class_map.csv
	Threads    int    // 0 lets the TFLite layer choose
	UseXNNPACK bool
}

// YAMNet is a loaded YAMNet classifier. Not goroutine-safe by itself; the
// Orchestrator serialises inference across all models, and the mutex here
// guards against a concurrent Close.
type YAMNet struct {
	mu         sync.Mutex
	classifier inference.Classifier
	labels     []string
	modelPath  string
	info       ModelInfo

	// frameBuf is reused across frames and windows. Predict is called once per
	// analysis window on a Raspberry Pi; allocating 15600 float32s each time is
	// not free at that rate.
	frameBuf []float32
	// scoreBuf accumulates the per-class maximum across frames.
	scoreBuf []float32
	// emit[i] reports whether class i is one SoundNet records. See reportable.
	emit []bool

	// SOUNDNET: state for the low-pass second pass. See yamnet_lowpass.go.
	//
	// aircraft[i] marks the classes that pass is allowed to raise; lowPassBuf
	// holds the filtered copy of the window, grown once and then reused, for the
	// same reason frameBuf is reused.
	aircraft   []bool
	lowPassBuf []float32
}

// reportable marks which of YAMNet's 521 classes are worth emitting.
//
// Emitting all of them floods the detection list with classes that are real but
// useless here: a live run produced "animal" at 0.96, "bird" at 0.92 and
// "whistling" at 0.99, three or four rows per window, burying the birds the
// operator actually wants alongside the events SoundNet exists to find.
//
// The event taxonomy already encodes which classes matter and which are
// deliberately "other" (see internal/eventclass). Using it here is what makes
// that table load-bearing rather than decorative.
//
// Matched on AudioSetIndex, not on the label text. The index is the join the
// taxonomy documents as authoritative, and the label forms differ either side:
// the taxonomy holds AudioSet display names ("Jet engine"), the adapter emits
// the normalised storage form ("jet_engine").
func reportable(numClasses int, labels []string) []bool {
	emit := make([]bool, numClasses)

	// The deliberate exception: classes the privacy and dog-bark filters act on.
	//
	// Those filters work by inspecting results as they pass through the
	// processor (handleHumanDetection / handleDogDetection), so a class the
	// adapter withholds is a class they can never see. Filtering YAMNet down to
	// the event taxonomy alone would therefore have silently weakened privacy
	// protection: speech is not an "event" and is not in the taxonomy, but
	// YAMNet recognises it at 0.98 where BirdNET's non-species Human label
	// rarely passes 0.2, and that accuracy is exactly what makes a sane privacy
	// threshold possible.
	//
	// Emitting them costs nothing in stored rows. A speech hit makes the privacy
	// filter discard the whole window, so the speech result is dropped along
	// with everything else in it.
	for i, label := range labels {
		if i >= numClasses {
			break
		}
		if vocalization.IsHuman(label) || vocalization.IsDog(label) {
			emit[i] = true
		}
	}

	for _, domain := range eventclass.AllDomains() {
		for _, c := range eventclass.InDomain(domain) {
			// A negative index is a classifier's own label (BirdNET's "Gun"),
			// not an AudioSet class, so it cannot appear in YAMNet's output.
			if c.AudioSetIndex < 0 || c.AudioSetIndex >= numClasses {
				continue
			}
			// DefaultEnabled is the taxonomy's own statement of what is worth
			// recording out of the box. Classes it maps but leaves disabled stay
			// addressable for a future config key without changing this.
			if c.DefaultEnabled {
				emit[c.AudioSetIndex] = true
			}
		}
	}
	return emit
}

// NewYAMNet loads YAMNet from its model file and class map.
func NewYAMNet(cfg *YAMNetConfig) (*YAMNet, error) {
	if cfg == nil || cfg.ModelPath == "" || cfg.LabelPath == "" {
		return nil, errors.Newf("YAMNet requires both a model path and a class map path").
			Component("classifier.yamnet").
			Category(errors.CategoryValidation).
			Build()
	}

	labels, err := loadYAMNetClassMap(cfg.LabelPath)
	if err != nil {
		return nil, err
	}

	modelData, err := os.ReadFile(cfg.ModelPath)
	if err != nil {
		return nil, errors.New(err).
			Component("classifier.yamnet").
			Category(errors.CategoryModelLoad).
			Context("model_path", cfg.ModelPath).
			Build()
	}

	log := GetLogger()
	classifier, threads, err := tflite.NewTFLiteClassifier(modelData, tflite.TFLiteClassifierOptions{
		Threads:    cfg.Threads,
		UseXNNPACK: cfg.UseXNNPACK,
		ErrorFunc: func(msg string) {
			log.Error("TFLite error", logger.String("message", msg), logger.String("model", RegistryIDYAMNet))
		},
		WarnFunc: func(msg string) {
			log.Warn(msg, logger.String("model", RegistryIDYAMNet))
		},
	})
	if err != nil {
		return nil, errors.New(err).
			Component("classifier.yamnet").
			Category(errors.CategoryModelInit).
			Context("model_path", cfg.ModelPath).
			Build()
	}

	// A class map that disagrees with the model is the one failure that would
	// otherwise stay silent: inference would succeed and every detection would
	// carry the wrong label. Refuse to load instead.
	if n := classifier.NumSpecies(); n != len(labels) {
		classifier.Close()
		return nil, errors.Newf("YAMNet class map has %d classes but the model emits %d", len(labels), n).
			Component("classifier.yamnet").
			Category(errors.CategoryModelInit).
			Context("labels_path", cfg.LabelPath).
			Build()
	}

	info := ModelRegistry[RegistryIDYAMNet]
	y := &YAMNet{
		classifier: classifier,
		labels:     labels,
		modelPath:  cfg.ModelPath,
		info:       info,
		frameBuf:   make([]float32, yamnetFrameSamples),
		aircraft:   aircraftClasses(len(labels)),
		scoreBuf:   make([]float32, len(labels)),
		emit:       reportable(len(labels), labels),
	}

	reported := 0
	for _, ok := range y.emit {
		if ok {
			reported++
		}
	}

	log.Info("YAMNet loaded",
		logger.String("model_path", cfg.ModelPath),
		logger.Int("classes", len(labels)),
		logger.Int("reported_classes", reported),
		logger.Int("threads", threads))
	return y, nil
}

// loadYAMNetClassMap reads the AudioSet class map and returns labels indexed by
// class index, normalised to the raw-label form the rest of the pipeline uses.
//
// Normalisation is not cosmetic. internal/labels/nonbird keys its table on that
// form, and a label that misses it is stored as a bird species with the Aves
// taxonomic class - so "Jet engine" left verbatim would be filed as a bird.
func loadYAMNetClassMap(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New(err).
			Component("classifier.yamnet").
			Category(errors.CategoryModelLoad).
			Context("labels_path", path).
			Build()
	}
	defer func() { _ = f.Close() }()

	r := csv.NewReader(f)
	header, err := r.Read()
	if err != nil {
		return nil, errors.New(err).
			Component("classifier.yamnet").
			Category(errors.CategoryValidation).
			Context("labels_path", path).
			Build()
	}
	if len(header) < 3 {
		return nil, errors.Newf("YAMNet class map header has %d columns, want index,mid,display_name", len(header)).
			Component("classifier.yamnet").
			Category(errors.CategoryValidation).
			Build()
	}

	// Indexed rather than appended: the file is ordered today, but the index
	// column is the authority, and silently trusting row order is exactly how a
	// class map ends up off by one.
	byIndex := map[int]string{}
	maxIndex := -1
	for {
		row, readErr := r.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, errors.New(readErr).
				Component("classifier.yamnet").
				Category(errors.CategoryValidation).
				Context("labels_path", path).
				Build()
		}
		idx, convErr := strconv.Atoi(strings.TrimSpace(row[0]))
		if convErr != nil {
			return nil, errors.New(convErr).
				Component("classifier.yamnet").
				Category(errors.CategoryValidation).
				Context("row", strings.Join(row, ",")).
				Build()
		}
		if _, dup := byIndex[idx]; dup {
			return nil, errors.Newf("YAMNet class map repeats index %d", idx).
				Component("classifier.yamnet").
				Category(errors.CategoryValidation).
				Build()
		}
		byIndex[idx] = NormaliseAudioSetLabel(row[2])
		maxIndex = max(maxIndex, idx)
	}

	if maxIndex < 0 {
		return nil, errors.Newf("YAMNet class map is empty").
			Component("classifier.yamnet").
			Category(errors.CategoryValidation).
			Build()
	}
	labels := make([]string, maxIndex+1)
	for i := range labels {
		label, ok := byIndex[i]
		if !ok {
			return nil, errors.Newf("YAMNet class map has no entry for index %d", i).
				Component("classifier.yamnet").
				Category(errors.CategoryValidation).
				Build()
		}
		labels[i] = label
	}
	return labels, nil
}

// NormaliseAudioSetLabel converts an AudioSet display name to the raw-label form
// the pipeline stores: lower case, comma-separated parts joined with "_and_",
// spaces as underscores.
//
//	"Jet engine"                 -> "jet_engine"
//	"Child speech, kid speaking" -> "child_speech_and_kid_speaking"
//
// The rule is upstream's, inferred from the keys already in
// internal/labels/nonbird and pinned by a test there.
func NormaliseAudioSetLabel(displayName string) string {
	parts := strings.Split(displayName, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return strings.ToLower(strings.ReplaceAll(strings.Join(parts, "_and_"), " ", "_"))
}

// Predict runs YAMNet over one analysis window.
//
// The window is 3 s but the model consumes 0.975 s, so the window is tiled with
// yamnetFramesPerClip evenly spaced frames and the per-class scores are combined
// by maximum.
//
// Maximum, not mean: a two-second siren inside a three-second window is a siren,
// and averaging would report it at a third of its strength. Events are sparse
// and local, which is the opposite of the assumption averaging makes.
func (y *YAMNet) Predict(ctx context.Context, samples [][]float32) ([]datastore.Results, error) {
	span, _ := startPredictSpan(ctx, RegistryIDYAMNet, samples)
	defer span.Finish()

	start := time.Now()

	if len(samples) == 0 || len(samples[0]) == 0 {
		span.markErrored(errTypeEmptySample)
		return nil, errors.Newf("empty audio sample").
			Component("classifier.yamnet").
			Category(errors.CategoryValidation).
			Build()
	}
	clip := samples[0]
	if len(clip) < yamnetFrameSamples {
		span.markErrored(errTypeEmptySample)
		return nil, errors.Newf("YAMNet needs at least %d samples, got %d", yamnetFrameSamples, len(clip)).
			Component("classifier.yamnet").
			Category(errors.CategoryValidation).
			Build()
	}

	y.mu.Lock()
	defer y.mu.Unlock()

	if y.classifier == nil {
		span.markErrored(errTypeClassifierNil)
		return nil, errors.Newf("YAMNet classifier is not initialized").
			Component("classifier.yamnet").
			Category(errors.CategoryModelInit).
			Build()
	}

	for i := range y.scoreBuf {
		y.scoreBuf[i] = 0
	}

	for _, offset := range yamnetFrameOffsets(len(clip), yamnetFrameSamples, yamnetFramesPerClip) {
		// Copy rather than reslice: the TFLite wrapper copies into the input
		// tensor, but process.go returns the sample slice to a pool on return,
		// and a reslice would tie this frame's lifetime to that.
		copy(y.frameBuf, clip[offset:offset+yamnetFrameSamples])

		scores, err := y.classifier.Predict(y.frameBuf)
		if err != nil {
			err = errors.New(err).
				Component("classifier.yamnet").
				Category(errors.CategoryAudio).
				Context("model", RegistryIDYAMNet).
				Context("frame_offset", offset).
				Build()
			recordPredictionFailure(span, RegistryIDYAMNet, errTypeInvokeFailed, start, err)
			return nil, err
		}
		if idx := firstNonFinite(scores); idx != noNonFiniteScore {
			// A NaN compares false against every threshold, so it is not dropped
			// downstream - it is promoted to a detection for whichever label sorts
			// first. Fail the window instead.
			err = newNonFiniteScoreError(nonFiniteScore{modelID: RegistryIDYAMNet, index: idx, count: len(scores)}, y.RuntimeInfo)
			recordPredictionFailure(span, RegistryIDYAMNet, errTypeNonFiniteLogits, start, err)
			return nil, err
		}
		if len(scores) != len(y.scoreBuf) {
			err := errors.Newf("YAMNet emitted %d scores, expected %d", len(scores), len(y.scoreBuf)).
				Component("classifier.yamnet").
				Category(errors.CategoryModelInit).
				Build()
			recordPredictionFailure(span, RegistryIDYAMNet, errTypeLabelMismatch, start, err)
			return nil, err
		}
		for i, s := range scores {
			y.scoreBuf[i] = max(y.scoreBuf[i], s)
		}
	}

	// SOUNDNET: a second pass over a low-passed copy, raising only the aircraft
	// classes. Failures here are not fatal: the raw scores are a complete,
	// correct result on their own, and losing a whole window because an
	// enhancement failed would trade a real detection for an optional one.
	if err := y.mergeLowPassPass(clip); err != nil {
		GetLogger().Warn("yamnet: low-pass pass failed, using raw scores",
			logger.Error(err),
			logger.String("operation", "yamnet_lowpass"))
	}

	// No activation applied. YAMNet's output is already per-class probability;
	// see the file comment.
	results := make([]datastore.Results, 0, defaultTopKResults*2)
	for i, s := range y.scoreBuf {
		if s < yamnetScoreFloor || !y.emit[i] {
			continue
		}
		results = append(results, datastore.Results{Species: y.labels[i], Confidence: s})
	}

	topResults := getTopKResults(results, defaultTopKResults)
	recordPredictionSuccess(span, len(topResults), start)
	return topResults, nil
}

// yamnetFrameOffsets returns evenly spaced frame start offsets tiling a clip.
//
// With the standard 48000-sample window and four 15600-sample frames the hop is
// 10800, so the frames start at 0, 10800, 21600 and 32400 and the last one ends
// exactly at 48000 - the whole window is covered, with no tail discarded and no
// frame reading past the end.
func yamnetFrameOffsets(clipLen, frameLen, frames int) []int {
	if frames < 1 || clipLen < frameLen {
		return nil
	}
	if frames == 1 || clipLen == frameLen {
		return []int{0}
	}
	span := clipLen - frameLen
	offsets := make([]int, frames)
	for i := range frames {
		// Round rather than truncate so the frames stay centred on the window
		// instead of drifting toward the start.
		offsets[i] = int(math.Round(float64(span) * float64(i) / float64(frames-1)))
	}
	return offsets
}

// Spec returns YAMNet's audio requirements as SoundNet feeds it: 16 kHz, 3 s.
func (y *YAMNet) Spec() ModelSpec { return y.info.Spec }

// ModelID returns the registry identifier.
func (y *YAMNet) ModelID() string { return RegistryIDYAMNet }

// ModelName returns the human-readable name.
func (y *YAMNet) ModelName() string { return y.info.Name }

// ModelVersion returns the model version string.
func (y *YAMNet) ModelVersion() string { return y.info.DetectionVersion }

// NumSpecies returns the number of AudioSet classes. Named "species" by the
// interface; for YAMNet they are sound classes, not taxa.
func (y *YAMNet) NumSpecies() int { return len(y.labels) }

// Labels returns a copy of the class labels.
func (y *YAMNet) Labels() []string {
	out := make([]string, len(y.labels))
	copy(out, y.labels)
	return out
}

// RuntimeInfo returns the compute device, backend and precision. Fixed at
// construction: TFLite is CPU-only here, and the XNNPACK delegate is still CPU.
func (y *YAMNet) RuntimeInfo() (device, backend, precision string) {
	return deviceCPU, BackendTFLite, string(y.info.Quantization)
}

// ResolvedModelPath returns the model file actually loaded.
func (y *YAMNet) ResolvedModelPath() string { return y.modelPath }

// Close releases the interpreter.
func (y *YAMNet) Close() error {
	y.mu.Lock()
	defer y.mu.Unlock()
	if y.classifier != nil {
		y.classifier.Close()
		y.classifier = nil
	}
	return nil
}
