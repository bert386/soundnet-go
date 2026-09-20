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
	"maps"
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

	// ResolvedDomain is the domain the identity came back under, which is not
	// always Domain. A "Vehicle" detection put to ADS-B and matched to an
	// overflight resolves as aircraft, and that difference is the whole point of
	// asking: it is the authority correcting a coarse acoustic reading.
	ResolvedDomain eventclass.Domain
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

	class, _ := eventclass.Resolve(in.Label)
	res.Domain = class.Domain

	var errs []error
	if a.Config.DiagnosticsEnabled && class.Domain.Diagnosable() {
		if err := a.runDiagnostics(in, class, res); err != nil {
			errs = append(errs, err)
		}
	}
	// class.Enrichable, not class.Domain.Enrichable: a label that does not
	// determine its own domain ("Vehicle", "Engine") must still reach the
	// authority for the domains it could be. See internal/eventclass/ambiguity.go.
	if a.Config.EnrichmentEnabled && class.Enrichable() {
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

	// Each domain the label could belong to, its own first, stopping at the first
	// authority that answers. Unambiguous classes have exactly one candidate, so
	// this is the previous single call with no extra work; the loop only costs
	// anything for "Vehicle" and "Engine".
	//
	// A domain with no registered provider returns ErrNoMatch without a network
	// call, which is what keeps the rail and watercraft candidates free.
	var (
		id       *enrichment.Identity
		resolved eventclass.Domain
		firstErr error
	)
	for _, domain := range class.CandidateDomains() {
		candidate, err := a.Resolver.Resolve(ctx, &enrichment.Request{
			Domain:     string(domain),
			Label:      in.Label,
			DetectedAt: in.DetectedAt,
			Confidence: in.Confidence,
			Station:    a.Config.Station,
		})
		switch {
		case errors.Is(err, enrichment.ErrNoMatch):
			continue
		case err != nil:
			// Keep asking the remaining domains: one provider being down should
			// not cost an answer another could have given. Reported only if
			// nothing resolves, so a real match is never masked by a stale error.
			if firstErr == nil {
				firstErr = err
			}
			continue
		case candidate == nil:
			continue
		}
		id, resolved = candidate, domain
		break
	}
	switch {
	case id == nil && firstErr != nil:
		return fmt.Errorf("eventpipeline: resolve identity: %w", firstErr)
	case id == nil:
		// The common and correct outcome for most detections. No row is written:
		// absence is the honest record, and an empty row would later be
		// indistinguishable from a real match.
		return nil
	}

	res.IdentityResolved = true
	res.ResolvedDomain = resolved
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
		Attributes:      withQueriedDomain(id.Attributes, class.Domain, resolved),
	})
	if err != nil {
		return fmt.Errorf("eventpipeline: build enrichment record: %w", err)
	}
	if err := a.Store.PutEnrichment(rec); err != nil {
		return fmt.Errorf("eventpipeline: store enrichment: %w", err)
	}
	return nil
}

// withQueriedDomain records which question the provider answered, when that is
// not the question the taxonomy would have asked.
//
// Stored in the provider's own attribute document rather than in a column,
// because it is diagnostic rather than structural: it explains, months later,
// why a detection the classifier called a vehicle carries an aircraft's
// registration. The provider's map is copied rather than written through - it
// belongs to the enrichment layer, and a pipeline that mutated it would be
// invisible to whoever reads that layer on its own.
func withQueriedDomain(attrs map[string]any, classified, resolved eventclass.Domain) map[string]any {
	if resolved == "" || resolved == classified {
		return attrs
	}
	out := make(map[string]any, len(attrs)+2)
	maps.Copy(out, attrs)
	out["soundnetClassifiedDomain"] = string(classified)
	out["soundnetResolvedDomain"] = string(resolved)
	return out
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

// Corroboration is what an authority could say about a detection that has not
// been recorded yet.
type Corroboration struct {
	// Matched reports whether an authority placed something it knows about in a
	// position that could have made this sound.
	Matched bool

	// Domain is the domain the match came back under, which for an ambiguous
	// label is often not the one the classifier implied.
	Domain eventclass.Domain

	// Provider names who answered, for the log line that explains why a quiet
	// detection was kept.
	Provider string
}

// Corroborate asks the authorities whether anything they know about could have
// made this sound, and records nothing.
//
// This exists because of an ordering problem the rest of the pipeline cannot
// solve. A distant aircraft scores 0.10-0.15, a sensible threshold is 0.7, so no
// detection is created - and because no detection is created, nothing ever
// reaches enrichment to discover that an aircraft really was overhead. The
// evidence that would justify keeping the detection is unreachable from behind
// the threshold that discards it.
//
// So this is deliberately callable before a detection exists. It needs only a
// label and a time: identity resolution is geometric, and the detection ID is
// required to *store* a result, not to obtain one.
//
// It is not a detector. A match means an authority places a credible source
// overhead at that moment, which is corroboration for a weak acoustic signal and
// nothing at all on its own - an aircraft passing over a silent garden is still
// not a detection.
func (a *Analyser) Corroborate(ctx context.Context, label string, at time.Time, confidence float64) (*Corroboration, error) {
	if a.Resolver == nil || !a.Config.EnrichmentEnabled {
		return &Corroboration{}, nil
	}
	if !a.Config.Station.Valid() {
		return &Corroboration{}, fmt.Errorf("eventpipeline: %w: station position", enrichment.ErrNotConfigured)
	}

	class, _ := eventclass.Resolve(label)
	var firstErr error
	for _, domain := range class.CandidateDomains() {
		if !domain.Enrichable() {
			// No authority exists for this domain, so there is nothing to ask and
			// no credit to spend asking it.
			continue
		}
		id, err := a.Resolver.Resolve(ctx, &enrichment.Request{
			Domain:     string(domain),
			Label:      label,
			DetectedAt: at,
			Confidence: confidence,
			Station:    a.Config.Station,
		})
		switch {
		case errors.Is(err, enrichment.ErrNoMatch):
			continue
		case err != nil:
			if firstErr == nil {
				firstErr = err
			}
			continue
		case id == nil:
			continue
		}
		return &Corroboration{Matched: true, Domain: domain, Provider: id.Provider}, nil
	}
	if firstErr != nil {
		// Reported rather than swallowed as "nothing overhead". A provider that is
		// down must not look like a quiet sky, or a lowered threshold would
		// silently stop admitting anything the moment the network failed.
		return &Corroboration{}, fmt.Errorf("eventpipeline: corroborate: %w", firstErr)
	}
	return &Corroboration{}, nil
}
