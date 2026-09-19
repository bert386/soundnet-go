// Package enrichment resolves the identity of a detection from an authoritative
// external source. It is the third of the three SoundNet layers: classification
// says what kind of thing, the acoustics package says what it was doing, and
// this says which exact one it was.
//
// The central rule is that identity comes from an authority or not at all.
// Aircraft have ADS-B; thunder has lightning networks. Sirens, gunshots and
// passing vehicles have no public authority, and for those a provider returns
// nothing. An invented identity would be indistinguishable downstream from a
// real one, which makes fabrication worse than silence.
//
// Providers are opt-in and disabled unless configured, because they make
// outbound network calls from a system that is otherwise local-only.
package enrichment

import (
	"context"
	"errors"
	"time"
)

// ErrNoMatch means the provider worked correctly and found nothing. It is an
// ordinary outcome, not a failure: most detections have no external identity.
// Callers must distinguish it from a genuine error, because one means "record
// no identity" and the other means "the provider is broken".
var ErrNoMatch = errors.New("enrichment: no authoritative match")

// ErrNotConfigured means the provider is enabled but lacks what it needs, such
// as station coordinates or credentials. Distinct from ErrNoMatch so that a
// misconfiguration surfaces as a setup problem rather than silently looking
// like a site where nothing ever flies over.
var ErrNotConfigured = errors.New("enrichment: provider not configured")

// Station is the listening position. Enrichment is inert without it: every
// geometric step needs to know where the microphone is.
type Station struct {
	Latitude   float64
	Longitude  float64
	ElevationM float64
}

// Valid reports whether the station has usable coordinates.
//
// The zero value is explicitly invalid. Null Island is in the Gulf of Guinea,
// and treating an unset configuration as a real position there would produce
// confident, entirely wrong matches rather than an obvious failure.
func (s Station) Valid() bool {
	if s.Latitude == 0 && s.Longitude == 0 {
		return false
	}
	return s.Latitude >= -90 && s.Latitude <= 90 && s.Longitude >= -180 && s.Longitude <= 180
}

// Request is what a provider is asked to resolve.
type Request struct {
	// Domain is the event family, from the eventclass package. Providers are
	// registered per domain so an aircraft query is never run for a gunshot.
	Domain string

	// Label is the classifier's label, for providers that discriminate within a
	// domain.
	Label string

	// DetectedAt is when the sound was *heard*. The emitting event happened
	// earlier by the propagation delay, which is what the lag correction undoes.
	DetectedAt time.Time

	// Confidence is the classifier's confidence, passed through so a provider
	// can decline to spend an API credit on a marginal detection.
	Confidence float64

	Station Station
}

// Identity is a resolved external identity.
type Identity struct {
	// Provider and Source name who answered and from where, because latency and
	// trustworthiness differ sharply between sources for the same provider.
	Provider string
	Source   string

	// Confidence is the provider's own confidence in this match, 0..1. An ADS-B
	// match with one aircraft in the sky is near 1; a crowded sky is lower.
	Confidence float64

	// LagCorrectionMs is the propagation delay applied before matching. Recorded
	// so a suspicious match can be diagnosed later without recomputing geometry.
	LagCorrectionMs int64

	// Attributes is the provider-specific identity document, stored as JSON.
	Attributes map[string]any
}

// Provider resolves identity for one domain.
type Provider interface {
	// Name identifies the provider ("adsb", "lightning").
	Name() string

	// Domains lists the event domains this provider can answer for. The registry
	// uses this so a provider is never asked about a domain it knows nothing of.
	Domains() []string

	// Resolve returns an identity, or ErrNoMatch when none was found. It must
	// never return a fabricated or best-guess identity.
	Resolve(ctx context.Context, req *Request) (*Identity, error)
}

// Registry dispatches requests to providers by domain.
type Registry struct {
	byDomain map[string][]Provider
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{byDomain: make(map[string][]Provider)}
}

// Register adds a provider for each domain it declares.
func (r *Registry) Register(p Provider) {
	for _, d := range p.Domains() {
		r.byDomain[d] = append(r.byDomain[d], p)
	}
}

// Resolve asks every provider registered for the request's domain, returning
// the first identity found.
//
// A domain with no registered provider returns ErrNoMatch rather than an error.
// That is the common case by design: most domains have no authority, and asking
// about one is not a mistake.
func (r *Registry) Resolve(ctx context.Context, req *Request) (*Identity, error) {
	providers := r.byDomain[req.Domain]
	if len(providers) == 0 {
		return nil, ErrNoMatch
	}
	var firstErr error
	for _, p := range providers {
		id, err := p.Resolve(ctx, req)
		switch {
		case err == nil && id != nil:
			return id, nil
		case errors.Is(err, ErrNoMatch):
			continue
		case err != nil && firstErr == nil:
			// Keep looking: one provider being down should not mask another's
			// answer. The error is reported only if nothing else resolves.
			firstErr = err
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, ErrNoMatch
}

// HasProviderFor reports whether any provider covers a domain. The UI uses this
// to distinguish "no identity found" from "identity is not possible here",
// which are very different things to show an operator.
func (r *Registry) HasProviderFor(domain string) bool {
	return len(r.byDomain[domain]) > 0
}
