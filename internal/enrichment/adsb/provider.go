package adsb

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/bert386/soundnet-go/internal/enrichment"
)

// Provider resolves aircraft identity from ADS-B state data.
type Provider struct {
	Source StateSource
	Config Config

	// Metadata optionally turns the broadcast identifiers into a description of
	// the aircraft and its flight. Optional by design: it is a third-party
	// lookup, so it is opt-in, and a failure here must never cost us the
	// identification itself. The hex code and callsign come from the aircraft;
	// everything Metadata adds is a lookup against someone else's database.
	Metadata MetadataResolver
}

// Config tunes matching.
type Config struct {
	// SearchRadiusM sets the bounding box around the station. It should exceed
	// MaxRangeM so an aircraft near the edge of credible audibility is still a
	// candidate, but not so far that the box costs extra credits.
	SearchRadiusM float64

	// MaxRangeM is the slant range beyond which a match is not credible. Site
	// dependent: a quiet rural station hears aircraft much further off than an
	// urban one.
	MaxRangeM float64

	// MinQuality is the match score below which nothing is returned. Set above
	// zero deliberately - a weak best candidate is usually no candidate at all.
	MinQuality float64

	// AmbiguityMargin is how far the best candidate must lead the runner-up.
	// With two aircraft equally plausible, naming one would be a coin toss
	// presented as a fact, so neither is returned.
	AmbiguityMargin float64
}

// DefaultConfig returns defaults suited to a rural station.
//
// MaxRangeM is 7km rather than something larger because that is roughly where
// MaxCredibleLag caps the acoustic-lag correction: beyond about 20 seconds of
// delay, back-projecting an aircraft's track stops being trustworthy.
func DefaultConfig() Config {
	return Config{
		SearchRadiusM:   12000,
		MaxRangeM:       7000,
		MinQuality:      0.25,
		AmbiguityMargin: 0.08,
	}
}

// Name identifies the provider.
func (p *Provider) Name() string { return "adsb" }

// Domains reports that this provider answers only for aircraft. Registering it
// narrowly is what stops a gunshot detection ever spending an API credit.
func (p *Provider) Domains() []string { return []string{"aircraft"} }

// candidate is one aircraft under consideration, with its corrected geometry.
type candidate struct {
	state   State
	emitted enrichment.Position
	lag     time.Duration
	slantM  float64
	quality float64
}

// Resolve identifies the aircraft most likely responsible for a detection.
//
// The sequence matters. Each aircraft's reported position is corrected for
// acoustic lag *before* being scored, because the sound was emitted from where
// the aircraft was seconds ago, not from where it is now. Scoring uncorrected
// positions would systematically favour whichever aircraft happens to be
// overhead at query time - which, for fast traffic, is frequently the wrong one.
func (p *Provider) Resolve(ctx context.Context, req *enrichment.Request) (*enrichment.Identity, error) {
	if req == nil {
		return nil, fmt.Errorf("adsb: nil request")
	}
	if !req.Station.Valid() {
		// Without a position there is no geometry, and therefore no way to tell
		// which aircraft was overhead. Reported as a configuration problem so it
		// does not masquerade as a site where nothing ever flies.
		return nil, enrichment.ErrNotConfigured
	}
	if p.Source == nil {
		return nil, fmt.Errorf("adsb: no state source configured")
	}

	cfg := p.Config
	if cfg.SearchRadiusM <= 0 {
		cfg = DefaultConfig()
	}

	latMin, lonMin, latMax, lonMax := boundingBox(req.Station, cfg.SearchRadiusM)
	states, err := p.Source.StatesInBox(ctx, latMin, lonMin, latMax, lonMax)
	if err != nil {
		return nil, fmt.Errorf("adsb: fetch states: %w", err)
	}

	candidates := make([]candidate, 0, len(states))
	for _, s := range states {
		if !s.HasPosition || s.OnGround || s.AltitudeSource == "none" {
			// Ground traffic and positionless contacts cannot be the source of an
			// airborne sound, and an aircraft with no altitude has no slant range.
			continue
		}
		reported := enrichment.Position{
			Latitude:      s.Latitude,
			Longitude:     s.Longitude,
			AltitudeM:     s.AltitudeM(),
			GroundSpeedMS: s.VelocityMS,
			TrackDeg:      s.TrackDeg,
		}
		emitted, lag := enrichment.CorrectForAcousticLag(req.Station, reported)
		if !enrichment.LagIsCredible(lag) {
			// Too far for the back-projection to be trusted. Skipped rather than
			// scored low: past this range the projected position can be kilometres
			// out, so the risk is a confident wrong answer, not a weak one.
			continue
		}
		q := enrichment.MatchQuality(req.Station, emitted, cfg.MaxRangeM)
		if q <= 0 {
			continue
		}
		candidates = append(candidates, candidate{
			state:   s,
			emitted: emitted,
			lag:     lag,
			slantM:  enrichment.SlantRangeM(req.Station, emitted),
			quality: q,
		})
	}

	if len(candidates) == 0 {
		return nil, enrichment.ErrNoMatch
	}

	best, runnerUp := topTwo(candidates)
	if best.quality < cfg.MinQuality {
		return nil, enrichment.ErrNoMatch
	}
	if runnerUp != nil && best.quality-runnerUp.quality < cfg.AmbiguityMargin {
		// Two aircraft equally plausible. Naming one would be a coin toss
		// presented as a fact, and a wrong identity is worse than none because
		// downstream it is indistinguishable from a correct one.
		return nil, enrichment.ErrNoMatch
	}

	// Confidence reflects both how good the match is and how clearly it beat the
	// alternatives: a lone aircraft in an empty sky is a far safer call than the
	// best of several.
	confidence := best.quality
	if runnerUp != nil {
		confidence *= 0.5 + 0.5*math.Min(1, (best.quality-runnerUp.quality)/0.3)
	}

	attrs := map[string]any{
		"hex":               best.state.ICAO24,
		"callsign":          best.state.Callsign,
		"altitude_m":        round1(best.state.AltitudeM()),
		"altitude_source":   best.state.AltitudeSource,
		"slant_range_km":    round2(best.slantM / 1000),
		"ground_speed_ms":   round1(best.state.VelocityMS),
		"track_deg":         round1(best.state.TrackDeg),
		"lag_correction_s":  round2(best.lag.Seconds()),
		"candidates_in_box": len(candidates),
	}

	// Identifiers the aircraft broadcast are authoritative; everything the
	// metadata lookup adds is a third-party claim. Marking the distinction keeps
	// a looked-up registration from being read as strongly as the hex code.
	attrs["identity_source"] = "broadcast"
	p.attachMetadata(ctx, &best.state, attrs)

	return &enrichment.Identity{
		Provider:        "adsb",
		Source:          "opensky",
		Confidence:      round2(confidence),
		LagCorrectionMs: best.lag.Milliseconds(),
		Attributes:      attrs,
	}, nil
}

// attachMetadata enriches the attributes with aircraft and route detail.
//
// Every failure is swallowed deliberately. The identification is already made
// and is the valuable part; losing it because a free community API was briefly
// unreachable would be a poor trade. Absent keys mean "not looked up or not
// found", which is why nothing is written as an empty string.
func (p *Provider) attachMetadata(ctx context.Context, s *State, attrs map[string]any) {
	if p.Metadata == nil {
		return
	}
	if info, err := p.Metadata.Aircraft(ctx, s.ICAO24); err == nil && info != nil {
		putIfSet(attrs, "registration", info.Registration)
		putIfSet(attrs, "type_code", info.TypeCode)
		putIfSet(attrs, "type_name", info.TypeName)
		putIfSet(attrs, "manufacturer", info.Manufacturer)
		putIfSet(attrs, "operator", info.Operator)
		putIfSet(attrs, "operator_country", info.OperatorCountry)
		attrs["metadata_source"] = "adsbdb"
	}
	if s.Callsign == "" {
		return
	}
	if route, err := p.Metadata.Route(ctx, s.Callsign); err == nil && route != nil {
		putIfSet(attrs, "flight_iata", route.FlightIATA)
		putIfSet(attrs, "flight_icao", route.FlightICAO)
		putIfSet(attrs, "airline", route.Airline)
		putIfSet(attrs, "origin_iata", route.OriginIATA)
		putIfSet(attrs, "origin_name", route.OriginName)
		putIfSet(attrs, "destination_iata", route.DestinationIATA)
		putIfSet(attrs, "destination_name", route.DestinationName)
		attrs["metadata_source"] = "adsbdb"
	}
}

// putIfSet writes a value only when it has one, so a missing field is absent
// rather than an empty string a consumer could mistake for a known blank.
func putIfSet(m map[string]any, key, value string) {
	if value != "" {
		m[key] = value
	}
}

// topTwo returns the best candidate and the runner-up, if any.
func topTwo(cs []candidate) (best, runnerUp *candidate) {
	for i := range cs {
		switch {
		case best == nil || cs[i].quality > best.quality:
			runnerUp = best
			best = &cs[i]
		case runnerUp == nil || cs[i].quality > runnerUp.quality:
			runnerUp = &cs[i]
		}
	}
	return best, runnerUp
}

// boundingBox returns a lat/lon box of roughly the given radius about a station.
//
// The longitude span is widened by the inverse cosine of latitude because a
// degree of longitude shrinks towards the poles; without it the box would be far
// too narrow east-west at the deployment station's 34 degrees south.
func boundingBox(s enrichment.Station, radiusM float64) (latMin, lonMin, latMax, lonMax float64) {
	const metresPerDegreeLat = 111195.0
	dLat := radiusM / metresPerDegreeLat
	cosLat := math.Cos(s.Latitude * math.Pi / 180)
	if math.Abs(cosLat) < 1e-6 {
		cosLat = 1e-6
	}
	dLon := radiusM / (metresPerDegreeLat * math.Abs(cosLat))
	return s.Latitude - dLat, s.Longitude - dLon, s.Latitude + dLat, s.Longitude + dLon
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }
