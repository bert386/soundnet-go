package classifier

// SOUNDNET: a minimal ONNX Runtime session for CED.
//
// CED does not go through inference.NewONNXClassifier, and the reason is worth
// recording because the first attempt did and failed on the station.
//
// That constructor wraps internal/inference/onnx, which auto-detects the model
// from its tensor shapes against an exhaustive table of BirdNET and Perch
// geometries - 144000 samples at 48 kHz, 160000 at 32 kHz, with one, two or four
// outputs. CED is 48000 samples at 16 kHz with one output and matches none of
// them, so it was rejected with "birdnet: cannot detect model type:
// unrecognized model". Rightly: that package is upstream's *species* classifier
// layer, and CED is not a species classifier. Teaching it about an acoustic
// event tagger would mean editing an upstream file to describe a fork model, and
// carrying BirdNET's SampleRate/Duration/embedding concepts into something that
// has none of them.
//
// A session of our own is smaller than that and says what it means: fixed input
// shape, fixed output shape, no detection, no abstraction. It satisfies
// inference.Classifier so the rest of the orchestrator is unchanged.

import (
	"fmt"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

// cedSession runs the fused CED graph: one 3-second waveform in, 527 AudioSet
// probabilities out.
type cedSession struct {
	mu      sync.Mutex
	session *ort.DynamicAdvancedSession
	classes int
}

// newCEDSession opens the model. The ONNX Runtime must already be initialised.
//
// The tensor names are those the fused export declares; they are part of the
// artefact's contract and are pinned by its checksum.
func newCEDSession(modelPath string, threads int) (*cedSession, error) {
	sessOpts, err := ort.NewSessionOptions()
	if err != nil {
		return nil, fmt.Errorf("ced: session options: %w", err)
	}
	defer func() { _ = sessOpts.Destroy() }()

	if threads <= 0 {
		threads = 1
	}
	if err := sessOpts.SetIntraOpNumThreads(threads); err != nil {
		return nil, fmt.Errorf("ced: intra-op threads: %w", err)
	}
	// Inter-op parallelism stays at one. The orchestrator already serialises
	// inference across models, so a second scheduler here would contend with
	// BirdNET rather than overlap with it.
	if err := sessOpts.SetInterOpNumThreads(1); err != nil {
		return nil, fmt.Errorf("ced: inter-op threads: %w", err)
	}

	session, err := ort.NewDynamicAdvancedSession(
		modelPath,
		[]string{"waveform"},
		[]string{"prob"},
		sessOpts,
	)
	if err != nil {
		return nil, fmt.Errorf("ced: create session: %w", err)
	}

	return &cedSession{session: session, classes: cedClasses}, nil
}

// Predict runs one window. samples must be exactly cedWindowSamples long; the
// graph's positional embeddings are baked to that length and a different one is
// refused by the runtime rather than silently mishandled.
func (s *cedSession) Predict(samples []float32) ([]float32, error) {
	if len(samples) != cedWindowSamples {
		return nil, fmt.Errorf("ced: got %d samples, want exactly %d", len(samples), cedWindowSamples)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil {
		return nil, fmt.Errorf("ced: session is closed")
	}

	// The input tensor wraps the caller's slice, so it must not outlive this
	// call; Destroy runs before we return either way.
	inputTensor, err := ort.NewTensor(ort.NewShape(1, int64(cedWindowSamples)), samples)
	if err != nil {
		return nil, fmt.Errorf("ced: build input tensor: %w", err)
	}
	defer func() { _ = inputTensor.Destroy() }()

	outputTensor, err := ort.NewEmptyTensor[float32](ort.NewShape(1, int64(s.classes)))
	if err != nil {
		return nil, fmt.Errorf("ced: build output tensor: %w", err)
	}
	defer func() { _ = outputTensor.Destroy() }()

	if err := s.session.Run([]ort.Value{inputTensor}, []ort.Value{outputTensor}); err != nil {
		return nil, fmt.Errorf("ced: run: %w", err)
	}

	// Copied out before the tensor is destroyed: GetData aliases runtime-owned
	// memory, and returning it would hand the caller a slice that the deferred
	// Destroy above frees.
	data := outputTensor.GetData()
	out := make([]float32, len(data))
	copy(out, data)
	return out, nil
}

// NumSpecies satisfies inference.Classifier. "Species" is upstream's word; for
// CED these are AudioSet event classes.
func (s *cedSession) NumSpecies() int { return s.classes }

// Close releases the session.
func (s *cedSession) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session != nil {
		_ = s.session.Destroy()
		s.session = nil
	}
}
