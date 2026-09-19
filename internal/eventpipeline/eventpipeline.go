// Package eventpipeline joins the SoundNet layers to a detection: it decides
// what kind of event was detected, measures its properties, attempts to
// identify it, and stores the results.
//
// It exists as its own package so the orchestration is testable without the
// detection processor, and so the hook into upstream stays a thin adapter with
// no logic in it. Everything decided here - which stages run, what counts as a
// failure, what gets stored - is decided in one place rather than scattered
// through upstream code.
package eventpipeline

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/bert386/soundnet-go/internal/acoustics"
	"github.com/bert386/soundnet-go/internal/enrichment"
	"github.com/bert386/soundnet-go/internal/eventclass"
	"github.com/bert386/soundnet-go/internal/eventrecord"
)

// Input is one detection to process.
type Input struct {
	// DetectionID links the results to the stored detection. Zero means the
	// detection was not persisted, in which case there is nothing to attach
	// results to and the work is skipped rather than done and discarded.
	DetectionID uint

	// Label is the classifier's label, used to resolve the event domain.
	Label string

	// Confidence is the classifier's confidence, passed to enrichment providers
	// so they can decline to spend an API credit on a marginal detection.
	Confidence float64

	// DetectedAt is when the sound was heard, not when it was emitted.
	DetectedAt time.Time

	// PCM is the raw clip, as the capture pipeline produced it.
	PCM         []byte
	SampleRate  int
	BitDepth    int
	NumChannels int

	// Bands are 1/3-octave levels from the existing sound level monitor, when
	// it is running. Optional: without them the near/far indicator is
	// unavailable, which the result reports rather than guesses at.
	Bands []acoustics.Band
}

// Config controls which stages run and how.
type Config struct {
	// DiagnosticsEnabled and EnrichmentEnabled gate the two layers
	// independently. Both default to off, as the scope requires for anything
	// that adds cost or makes outbound calls.
	DiagnosticsEnabled bool
	EnrichmentEnabled  bool

	Station enrichment.Station

	Level   acoustics.LevelConfig
	Onset   acoustics.OnsetConfig
	Doppler acoustics.DopplerConfig
	Impulse acoustics.ImpulseConfig

	// EnvelopeFrameSec and EnvelopeDropDB tune duration measurement.
	EnvelopeFrameSec float64
	EnvelopeDropDB   float64
}

// DefaultConfig returns defaults with both layers disabled, matching the
// scope's rule that new behaviour defaults off where it adds cost.
func DefaultConfig() Config {
	return Config{
		Level:            acoustics.DefaultLevelConfig(),
		Onset:            acoustics.DefaultOnsetConfig(),
		Doppler:          acoustics.DefaultDopplerConfig(),
		Impulse:          acoustics.DefaultImpulseConfig(),
		EnvelopeFrameSec: 0.01,
		EnvelopeDropDB:   20,
	}
}

// Store is the persistence this pipeline needs. An interface rather than the
// concrete type so the orchestration can be tested without a database.
type Store interface {
	PutDiagnostics(detectionID uint, doc any, schemaVersion int, computeMs int64) error
	PutEnrichment(e *eventrecord.Enrichment) error
}

// Resolver is the enrichment entry point, kept as an interface for the same
// reason.
type Resolver interface {
	Resolve(ctx context.Context, req *enrichment.Request) (*enrichment.Identity, error)
	HasProviderFor(domain string) bool
}

// Analyser runs the SoundNet layers for a detection.
type Analyser struct {
	Config   Config
	Store    Store
	Resolver Resolver
}

// Result summarises what happened, for logging and for tests. It is returned
// rather than only logged so a caller can report honestly what ran.
type Result struct {
	Domain           eventclass.Domain
	DiagnosticsRun   bool
	DiagnosticsMs    int64
	EnrichmentRun    bool
	IdentityResolved bool
	Skipped          string
}

// Process runs the enabled layers for one detection.
//
// Errors from a layer are returned, but a failure in one does not prevent the
// other from running: diagnostics and identity are independent, and losing both
// because one failed would be needless. The detection itself is already saved by
// upstream before this runs, so nothing here can lose a detection.
func (a *Analyser) Process(ctx context.Context, in *Input) (*Result, error) {
	res := &Result{}
	if in == nil {
		return res, errors.New("eventpipeline: nil input")
	}
	if in.DetectionID == 0 {
		// Nothing to attach results to. Doing the work anyway would burn Pi
		// cycles to produce something that is immediately discarded.
		res.Skipped = "detection was not persisted"
		return res, nil
	}

	class, _ := eventclass.Lookup(in.Label)
	res.Domain = class.Domain

	var errs []error
	if a.Config.DiagnosticsEnabled && class.Domain.Diagnosable() {
		if err := a.runDiagnostics(in, class, res); err != nil {
			errs = append(errs, err)
		}
	}
	if a.Config.EnrichmentEnabled && class.Domain.Enrichable() {
		if err := a.runEnrichment(ctx, in, class, res); err != nil {
			errs = append(errs, err)
		}
	}
	if res.Skipped == "" && !res.DiagnosticsRun && !res.EnrichmentRun {
		res.Skipped = fmt.Sprintf("no stage applies to domain %q", class.Domain)
	}
	return res, errors.Join(errs...)
}

// runDiagnostics measures the clip's properties and stores them.
func (a *Analyser) runDiagnostics(in *Input, class eventclass.Class, res *Result) error {
	samples, err := DecodePCM(in.PCM, in.BitDepth, in.NumChannels)
	if err != nil {
		return fmt.Errorf("eventpipeline: decode clip: %w", err)
	}
	if len(samples) == 0 {
		return nil
	}

	start := time.Now()
	doc := acoustics.Document{SchemaVersion: acoustics.SchemaVersion}

	// Level always runs for a diagnosable domain: it is cheap, and it is the
	// only stage that says anything when the others decline.
	doc.Level = acoustics.AnalyseLevel(samples, in.Bands, a.Config.Level)

	switch class.Domain {
	case eventclass.DomainImpulse:
		doc.Onsets = acoustics.AnalyseOnsets(samples, in.SampleRate, a.Config.Onset)
		doc.Impulse = acoustics.AnalyseImpulse(samples, in.SampleRate, a.Config.Impulse)
	case eventclass.DomainVehicle, eventclass.DomainAircraft, eventclass.DomainRail:
		// Doppler only makes sense for something that passes by. A stationary
		// source produces no shift, and the analyser reports that rather than
		// inventing a speed.
		if d, why := acoustics.AnalyseDoppler(samples, in.SampleRate, a.Config.Doppler); d != nil {
			doc.Doppler = d
		} else if why != "" {
			doc.Warnings = append(doc.Warnings, "doppler: "+why)
		}
	case eventclass.DomainWeather:
		doc.Envelope = acoustics.AnalyseEnvelope(samples, in.SampleRate, a.Config.EnvelopeFrameSec, a.Config.EnvelopeDropDB)
	case eventclass.DomainTool:
		doc.Onsets = acoustics.AnalyseOnsets(samples, in.SampleRate, a.Config.Onset)
		doc.Envelope = acoustics.AnalyseEnvelope(samples, in.SampleRate, a.Config.EnvelopeFrameSec, a.Config.EnvelopeDropDB)
	case eventclass.DomainAlarm, eventclass.DomainWatercraft,
		eventclass.DomainBiological, eventclass.DomainOther:
		// Level only. A siren, a boat or a bird has no transient to count, no
		// pass-by geometry to fit and no decay worth timing, so the extra stages
		// would spend Raspberry Pi cycles producing nothing. Listed explicitly
		// rather than left to a default so that adding a domain forces a decision
		// about what to measure for it.
	}

	elapsed := time.Since(start).Milliseconds()
	res.DiagnosticsRun = true
	res.DiagnosticsMs = elapsed

	if a.Store == nil {
		return nil
	}
	if err := a.Store.PutDiagnostics(in.DetectionID, doc, acoustics.SchemaVersion, elapsed); err != nil {
		return fmt.Errorf("eventpipeline: store diagnostics: %w", err)
	}
	return nil
}

// runEnrichment attempts external identification and stores any result.
func (a *Analyser) runEnrichment(ctx context.Context, in *Input, class eventclass.Class, res *Result) error {
	if a.Resolver == nil {
		return nil
	}
	if !a.Config.Station.Valid() {
		// Without a position there is no geometry. Reported so a missing setting
		// does not look like a site where nothing is ever identifiable.
		return fmt.Errorf("eventpipeline: %w: station position", enrichment.ErrNotConfigured)
	}
	res.EnrichmentRun = true

	id, err := a.Resolver.Resolve(ctx, &enrichment.Request{
		Domain:     string(class.Domain),
		Label:      in.Label,
		DetectedAt: in.DetectedAt,
		Confidence: in.Confidence,
		Station:    a.Config.Station,
	})
	switch {
	case errors.Is(err, enrichment.ErrNoMatch):
		// The common and correct outcome for most detections. No row is written:
		// absence is the honest record, and an empty row would later be
		// indistinguishable from a real match.
		return nil
	case err != nil:
		return fmt.Errorf("eventpipeline: resolve identity: %w", err)
	case id == nil:
		return nil
	}

	res.IdentityResolved = true
	if a.Store == nil {
		return nil
	}
	// Translate across the layer boundary. The storage package deliberately does
	// not import the enrichment package, so the conversion happens here, in the
	// one place that knows about both.
	rec, err := eventrecord.NewEnrichmentRecord(in.DetectionID, &eventrecord.Identity{
		Provider:        id.Provider,
		Source:          id.Source,
		Confidence:      id.Confidence,
		LagCorrectionMs: id.LagCorrectionMs,
		Attributes:      id.Attributes,
	})
	if err != nil {
		return fmt.Errorf("eventpipeline: build enrichment record: %w", err)
	}
	if err := a.Store.PutEnrichment(rec); err != nil {
		return fmt.Errorf("eventpipeline: store enrichment: %w", err)
	}
	return nil
}

// DecodePCM converts raw capture bytes to normalised mono samples.
//
// Multi-channel input is averaged rather than taking the first channel: with a
// stereo microphone both channels carry the event, and averaging improves the
// signal-to-noise ratio slightly rather than discarding half the data.
func DecodePCM(pcm []byte, bitDepth, channels int) ([]float64, error) {
	if channels <= 0 {
		channels = 1
	}
	switch bitDepth {
	case 16:
		frameBytes := 2 * channels
		if frameBytes == 0 || len(pcm) < frameBytes {
			return nil, nil
		}
		n := len(pcm) / frameBytes
		out := make([]float64, n)
		for i := range n {
			var sum float64
			for c := range channels {
				off := i*frameBytes + c*2
				v := int16(binary.LittleEndian.Uint16(pcm[off : off+2]))
				sum += float64(v) / math.MaxInt16
			}
			out[i] = sum / float64(channels)
		}
		return out, nil
	default:
		// Refusing an unknown format is better than guessing at it: a wrong
		// interpretation produces plausible-looking numbers from noise.
		return nil, fmt.Errorf("eventpipeline: unsupported bit depth %d", bitDepth)
	}
}
