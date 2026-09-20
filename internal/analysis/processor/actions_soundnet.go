package processor

// SoundNet integration point.
//
// This file is the ONLY addition to the upstream processor package, and it is
// deliberately an adapter with no logic of its own: it converts a detection into
// the pipeline's input shape and hands it over. All decisions about which stages
// run, what counts as a failure and what gets stored live in
// internal/eventpipeline, so upstream files stay as close to untouched as
// possible and merges stay cheap.
//
// The single edit to an existing upstream file is one append in
// getDefaultActions, marked with a SOUNDNET comment.

import (
	"context"
	"sync"
	"time"

	"github.com/bert386/soundnet-go/internal/conf"
	"github.com/bert386/soundnet-go/internal/detection"
	"github.com/bert386/soundnet-go/internal/eventpipeline"
	"github.com/bert386/soundnet-go/internal/logger"
)

// SoundNetAction attaches diagnostics and external identity to a saved detection.
type SoundNetAction struct {
	Analyser *eventpipeline.Analyser

	// DetectionCtx carries the database-assigned detection ID, populated by
	// DatabaseAction earlier in the sequence. Without an ID there is nothing to
	// attach results to, which is why this action must run after the save.
	//
	// "After the save" means inside the CompositeAction that holds it. This was
	// documented here from the start and the action was still appended at the top
	// level, where the job queue runs it as an independent task concurrently with
	// the save - so it always read zero and always returned early, silently, for
	// every detection since M3 shipped. The ordering is now asserted by a test
	// (actions_soundnet_order_test.go), because a comment did not hold it.
	DetectionCtx *DetectionContext

	Label      string
	Confidence float64
	DetectedAt time.Time
	PCM        []byte
	SampleRate int

	CorrelationID string
	Description   string
}

// GetDescription satisfies the Action interface.
func (a *SoundNetAction) GetDescription() string {
	if a.Description != "" {
		return a.Description
	}
	return "Compute SoundNet diagnostics and resolve external identity"
}

// Execute runs the SoundNet layers for this detection.
//
// It never returns an error that would fail the detection. By the time this
// runs the detection is already saved, and diagnostics and identity are
// additions to it: failing the action would make a missing aircraft lookup look
// like a failed detection in the logs, and could trigger retries of work that
// has already succeeded.
func (a *SoundNetAction) Execute(ctx context.Context, _ any) error {
	if a.Analyser == nil || a.DetectionCtx == nil {
		return nil
	}
	detectionID := uint(a.DetectionCtx.NoteID.Load())
	if detectionID == 0 {
		// Reported rather than swallowed. This is the state the whole layer sat
		// in unnoticed, and it is indistinguishable from "nothing was overhead"
		// unless it says so.
		GetLogger().Warn("soundnet: no detection id, skipping analysis",
			logger.String("correlation_id", a.CorrelationID),
			logger.String("label", a.Label),
			logger.String("operation", "soundnet_no_detection_id"))
		return nil
	}

	// Bounded below the composite's own per-action timeout so this action is
	// never the step that trips it. Enrichment makes an outbound call, and the
	// honest outcome of a slow one is no identity - not a failed detection and
	// not a timeout attributed to the sequence as a whole.
	ctx, cancel := context.WithTimeout(ctx, soundNetBudget)
	defer cancel()

	res, err := a.Analyser.Process(ctx, &eventpipeline.Input{
		DetectionID: detectionID,
		Label:       a.Label,
		Confidence:  a.Confidence,
		DetectedAt:  a.DetectedAt,
		PCM:         a.PCM,
		SampleRate:  a.SampleRate,
		BitDepth:    conf.BitDepth,
		NumChannels: conf.NumChannels,
	})
	if err != nil {
		GetLogger().Warn("soundnet: analysis incomplete",
			logger.String("correlation_id", a.CorrelationID),
			logger.String("label", a.Label),
			logger.Error(err))
		return nil
	}
	switch {
	case res == nil:
	case res.DiagnosticsRun || res.IdentityResolved:
		GetLogger().Debug("soundnet: detection analysed",
			logger.String("correlation_id", a.CorrelationID),
			logger.String("domain", string(res.Domain)),
			logger.String("resolved_domain", string(res.ResolvedDomain)),
			logger.Bool("diagnostics", res.DiagnosticsRun),
			logger.Bool("identity", res.IdentityResolved),
			logger.Int64("diagnostics_ms", res.DiagnosticsMs))
	case res.Skipped != "":
		// The reason was always computed and never logged, which is why a layer
		// that did nothing at all looked exactly like a quiet one. Debug because
		// the ordinary case - a bird, whose domain has nothing to measure and no
		// authority to ask - is most detections.
		GetLogger().Debug("soundnet: nothing to do for this detection",
			logger.String("correlation_id", a.CorrelationID),
			logger.String("domain", string(res.Domain)),
			logger.String("reason", res.Skipped),
			logger.String("operation", "soundnet_skipped"))
	}
	return nil
}

// soundNetBudget caps one detection's analysis.
//
// Deliberately shorter than CompositeActionTimeout: this action runs as the last
// step of that sequence, and a step which overruns is logged as a composite
// timeout - an error about the whole detection pipeline, raised by an optional
// addition to it. Bounding the work here keeps the failure where it belongs.
const soundNetBudget = CompositeActionTimeout - 2*time.Second

// soundNetAnalyser is the process-wide SoundNet analyser.
//
// Package-level rather than a Processor field so that enabling SoundNet costs
// exactly one line in an upstream file. The trade is deliberate: a field would
// be tidier, but every upstream struct this fork widens is a merge conflict in
// a file that changes often.
var (
	soundNetOnce     sync.Once
	soundNetAnalyser *eventpipeline.Analyser
)

// ConfigureSoundNet installs the analyser used by SoundNetAction. It is called
// once during startup wiring; calling it again has no effect.
func ConfigureSoundNet(a *eventpipeline.Analyser) {
	soundNetOnce.Do(func() { soundNetAnalyser = a })
}

// buildSoundNetAction returns the action for a detection, or nil when SoundNet
// is not configured or the clip is unavailable.
//
// Returning nil rather than an inert action keeps the action list honest: a
// no-op in the sequence would show up in logs and metrics as work that happened.
func (p *Processor) buildSoundNetAction(det *Detections, detectionCtx *DetectionContext) Action {
	if soundNetAnalyser == nil || det == nil || len(det.pcmData3s) == 0 {
		return nil
	}
	return &SoundNetAction{
		Analyser:     soundNetAnalyser,
		DetectionCtx: detectionCtx,
		// RawLabel is the classifier's own un-truncated label, which is what the
		// event taxonomy matches against. CommonName may have been rewritten for
		// display, and a rewritten label would silently fail to resolve.
		Label:         soundNetLabel(&det.Result),
		Confidence:    det.Result.Confidence,
		DetectedAt:    det.Result.BeginTime,
		PCM:           det.pcmData3s,
		SampleRate:    conf.SampleRate,
		CorrelationID: det.CorrelationID,
	}
}

// soundNetLabel picks the label the event taxonomy should match on.
//
// The raw classifier label is preferred over the display name: display names go
// through localisation and species-name normalisation, and a rewritten label
// would silently resolve to no event class at all rather than failing loudly.
func soundNetLabel(r *detection.Result) string {
	if r.RawLabel != "" {
		return r.RawLabel
	}
	return r.Species.CommonName
}
