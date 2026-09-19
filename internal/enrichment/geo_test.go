package enrichment_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bert386/soundnet-go/internal/enrichment"
)

// The real deployment station: the microphone's position and elevation in New
// South Wales. Elevation is not decoration - it feeds slant range directly, so
// every acoustic-lag correction is biased by an error here.
var station = enrichment.Station{Latitude: -34.11159024409095, Longitude: 150.7922555571461, ElevationM: 140}

func TestStationValidity(t *testing.T) {
	t.Parallel()

	assert.True(t, station.Valid())
	// The zero value must not be treated as a real position. Null Island is in
	// the Gulf of Guinea; an unset config there would produce confident,
	// completely wrong matches instead of an obvious failure.
	assert.False(t, enrichment.Station{}.Valid(), "unset coordinates must be invalid, not Null Island")
	assert.False(t, enrichment.Station{Latitude: 91, Longitude: 0}.Valid())
	assert.False(t, enrichment.Station{Latitude: 51.5, Longitude: 181}.Valid())
}

func TestHorizontalDistanceAgainstKnownValue(t *testing.T) {
	t.Parallel()

	// One degree of latitude is almost exactly 111.2 km anywhere on Earth.
	d := enrichment.HorizontalDistanceM(-35.0, 150.79, -34.0, 150.79)
	assert.InDelta(t, 111195, d, 500)

	// Same point is zero distance.
	assert.InDelta(t, 0, enrichment.HorizontalDistanceM(-34.11159, 150.79226, -34.11159, 150.79226), 0.001)
}

func TestSlantRangeUsesAltitude(t *testing.T) {
	t.Parallel()

	// Directly overhead at 3000m: the slant range is the height difference, and
	// the horizontal distance contributes nothing.
	overhead := enrichment.Position{Latitude: -34.11159024409095, Longitude: 150.7922555571461, AltitudeM: 3140}
	assert.InDelta(t, 3000, enrichment.SlantRangeM(station, overhead), 1)

	// The same altitude 4km away horizontally: 3-4-5 triangle, so 5km.
	offset := enrichment.Position{Latitude: -34.11159024409095, Longitude: 150.7922555571461 + 4000/(111195*0.8280), AltitudeM: 3140}
	assert.InDelta(t, 5000, enrichment.SlantRangeM(station, offset), 150)
}

func TestAcousticLagMatchesKnownGeometry(t *testing.T) {
	t.Parallel()

	// An aircraft overhead at 10,000 ft (3048 m) is about 8.9 seconds of sound
	// travel away. This is the number that makes lag correction necessary: an
	// uncorrected match would look 9 seconds into the wrong part of the sky.
	overhead := enrichment.Position{Latitude: -34.11159024409095, Longitude: 150.7922555571461, AltitudeM: 3048 + 140}
	lag := enrichment.AcousticLag(station, overhead)
	assert.InDelta(t, 8.89, lag.Seconds(), 0.1)

	// Low and close: under a second.
	low := enrichment.Position{Latitude: -34.11159024409095, Longitude: 150.7922555571461, AltitudeM: 340}
	assert.Less(t, enrichment.AcousticLag(station, low).Seconds(), 1.0)
}

func TestBackProjectRewindsAlongTrack(t *testing.T) {
	t.Parallel()

	// Due north at 200 m/s for 10 seconds: it was 2 km south of where it is now.
	p := enrichment.Position{Latitude: -34.11159024409095, Longitude: 150.7922555571461, AltitudeM: 3000, GroundSpeedMS: 200, TrackDeg: 0}
	was := enrichment.BackProject(p, 10*time.Second)

	assert.Less(t, was.Latitude, p.Latitude, "flying north means it came from the south")
	assert.InDelta(t, 2000, enrichment.HorizontalDistanceM(p.Latitude, p.Longitude, was.Latitude, was.Longitude), 50)

	// Due east: it was to the west, and latitude barely changes.
	east := enrichment.Position{Latitude: -34.11159024409095, Longitude: 150.7922555571461, AltitudeM: 3000, GroundSpeedMS: 200, TrackDeg: 90}
	wasEast := enrichment.BackProject(east, 10*time.Second)
	assert.Less(t, wasEast.Longitude, east.Longitude)
	assert.InDelta(t, east.Latitude, wasEast.Latitude, 0.001)
}

func TestBackProjectStationaryOrZeroDuration(t *testing.T) {
	t.Parallel()

	p := enrichment.Position{Latitude: -34.11159024409095, Longitude: 150.7922555571461, AltitudeM: 100}
	assert.Equal(t, p, enrichment.BackProject(p, 10*time.Second), "no speed means no movement")
	moving := enrichment.Position{Latitude: -34.11159024409095, Longitude: 150.7922555571461, GroundSpeedMS: 200, TrackDeg: 45}
	assert.Equal(t, moving, enrichment.BackProject(moving, 0))
}

func TestLagCorrectionConvergesAndMovesTheAircraft(t *testing.T) {
	t.Parallel()

	// A fast jet overhead: 250 m/s at 10,000 ft. Sound takes ~9 seconds to
	// arrive, during which it travels over 2 km - far enough that matching on
	// the uncorrected time can pick a different aircraft entirely.
	reported := enrichment.Position{
		Latitude: -34.11159024409095, Longitude: 150.7922555571461, AltitudeM: 3188,
		GroundSpeedMS: 250, TrackDeg: 90,
	}

	emitted, lag := enrichment.CorrectForAcousticLag(station, reported)

	// Naively, an aircraft 3048m overhead is 8.9s of sound travel away. The
	// converged answer is larger, and correctly so: by the time the sound
	// arrived the jet had flown on, meaning it emitted from further away than
	// straight overhead. Feeding back that extra distance is exactly what the
	// iteration is for, and ignoring it would search the wrong patch of sky.
	assert.InDelta(t, 12.0, lag.Seconds(), 0.6)
	assert.Greater(t, lag.Seconds(), 3048/enrichment.SpeedOfSound,
		"the emission point is further than straight overhead, so the lag must exceed the naive estimate")
	moved := enrichment.HorizontalDistanceM(reported.Latitude, reported.Longitude, emitted.Latitude, emitted.Longitude)
	assert.InDelta(t, 3000, moved, 300, "at 250 m/s a ~12s lag displaces the aircraft about 3km")
	assert.Less(t, emitted.Longitude, reported.Longitude, "flying east, it emitted the sound further west")
}

func TestLagCorrectionIsStableForSlowLowTraffic(t *testing.T) {
	t.Parallel()

	// A light aircraft low and slow: the correction should be small, not a
	// large swing. This guards against an iteration that diverges.
	reported := enrichment.Position{
		Latitude: -34.11159024409095, Longitude: 150.7922555571461, AltitudeM: 440,
		GroundSpeedMS: 50, TrackDeg: 180,
	}
	emitted, lag := enrichment.CorrectForAcousticLag(station, reported)
	assert.Less(t, lag.Seconds(), 1.5)
	moved := enrichment.HorizontalDistanceM(reported.Latitude, reported.Longitude, emitted.Latitude, emitted.Longitude)
	assert.Less(t, moved, 100.0)
}

func TestMatchQualityPrefersCloseAndOverhead(t *testing.T) {
	t.Parallel()
	const maxRange = 20000.0

	overhead := enrichment.Position{Latitude: -34.11159024409095, Longitude: 150.7922555571461, AltitudeM: 2140}
	distant := enrichment.Position{Latitude: -34.01, Longitude: 150.99, AltitudeM: 2140}
	assert.Greater(t, enrichment.MatchQuality(station, overhead, maxRange),
		enrichment.MatchQuality(station, distant, maxRange))

	// Beyond the credible range, a match scores nothing at all rather than a
	// small positive number that could still win if nothing else is in the sky.
	veryFar := enrichment.Position{Latitude: -33.11, Longitude: 150.79, AltitudeM: 10000}
	assert.InDelta(t, 0.0, enrichment.MatchQuality(station, veryFar, maxRange), 1e-9)
}

// --- registry behaviour ---

type stubProvider struct {
	name    string
	domains []string
	id      *enrichment.Identity
	err     error
	calls   *int
}

func (s stubProvider) Name() string      { return s.name }
func (s stubProvider) Domains() []string { return s.domains }
func (s stubProvider) Resolve(context.Context, *enrichment.Request) (*enrichment.Identity, error) {
	if s.calls != nil {
		*s.calls++
	}
	return s.id, s.err
}

func TestRegistryDispatchesByDomain(t *testing.T) {
	t.Parallel()
	calls := 0
	r := enrichment.NewRegistry()
	r.Register(stubProvider{
		name: "adsb", domains: []string{"aircraft"}, calls: &calls,
		id: &enrichment.Identity{Provider: "adsb", Confidence: 0.9},
	})

	got, err := r.Resolve(t.Context(), &enrichment.Request{Domain: "aircraft"})
	require.NoError(t, err)
	assert.Equal(t, "adsb", got.Provider)
	assert.Equal(t, 1, calls)
}

func TestRegistryReturnsNoMatchForUnservedDomain(t *testing.T) {
	t.Parallel()
	calls := 0
	r := enrichment.NewRegistry()
	r.Register(stubProvider{name: "adsb", domains: []string{"aircraft"}, calls: &calls})

	// A gunshot has no authority. This is the common case by design, so it must
	// be an ordinary ErrNoMatch and must not call the aircraft provider.
	_, err := r.Resolve(t.Context(), &enrichment.Request{Domain: "impulse"})
	require.ErrorIs(t, err, enrichment.ErrNoMatch)
	assert.Equal(t, 0, calls, "a provider must not be asked about a domain it does not serve")
	assert.False(t, r.HasProviderFor("impulse"))
	assert.True(t, r.HasProviderFor("aircraft"))
}

func TestRegistryKeepsLookingAfterAProviderFails(t *testing.T) {
	t.Parallel()
	r := enrichment.NewRegistry()
	r.Register(stubProvider{name: "broken", domains: []string{"aircraft"}, err: errors.New("network down")})
	r.Register(stubProvider{
		name: "working", domains: []string{"aircraft"},
		id: &enrichment.Identity{Provider: "working"},
	})

	// One source being unreachable must not mask another's answer.
	got, err := r.Resolve(t.Context(), &enrichment.Request{Domain: "aircraft"})
	require.NoError(t, err)
	assert.Equal(t, "working", got.Provider)
}

func TestRegistrySurfacesErrorWhenNothingResolves(t *testing.T) {
	t.Parallel()
	r := enrichment.NewRegistry()
	r.Register(stubProvider{name: "broken", domains: []string{"aircraft"}, err: errors.New("network down")})

	// A provider that is down must not look like a quiet sky: that distinction
	// is the difference between a setup problem and a site with no overflights.
	_, err := r.Resolve(t.Context(), &enrichment.Request{Domain: "aircraft"})
	require.Error(t, err)
	assert.NotErrorIs(t, err, enrichment.ErrNoMatch)
}

func TestLagCredibilityBound(t *testing.T) {
	t.Parallel()

	// An aircraft overhead at 3 km: about 9 seconds, comfortably trustworthy.
	overhead := enrichment.Position{Latitude: -34.11159024409095, Longitude: 150.7922555571461, AltitudeM: 3140}
	assert.True(t, enrichment.LagIsCredible(enrichment.AcousticLag(station, overhead)))

	// A live OpenSky sample at this station had an airliner at 14.7km slant
	// range - a 42.9 second delay, over which a jet covers nearly 11km. Rewinding
	// a straight track that far is not safe, so it must be rejected rather than
	// matched with reduced confidence: the failure mode is a confident wrong
	// answer, not a weak one.
	distant := enrichment.Position{Latitude: -34.0, Longitude: 150.95, AltitudeM: 2292}
	lag := enrichment.AcousticLag(station, distant)
	assert.Greater(t, lag.Seconds(), 40.0)
	assert.False(t, enrichment.LagIsCredible(lag))

	assert.False(t, enrichment.LagIsCredible(0))
}
