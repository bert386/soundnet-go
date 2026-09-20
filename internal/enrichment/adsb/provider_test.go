package adsb_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bert386/soundnet-go/internal/enrichment"
	"github.com/bert386/soundnet-go/internal/enrichment/adsb"
)

// The real deployment station.
var station = enrichment.Station{
	Latitude:   -34.11159024409095,
	Longitude:  150.7922555571461,
	ElevationM: 140,
}

// fixtureSource serves captured states, so tests exercise the real decoding and
// matching logic without touching the network.
type fixtureSource struct {
	states []adsb.State
	err    error
	calls  int
}

func (f *fixtureSource) StatesInBox(context.Context, float64, float64, float64, float64) ([]adsb.State, error) {
	f.calls++
	return f.states, f.err
}

func loadFixture(t *testing.T) []adsb.State {
	t.Helper()
	body, err := os.ReadFile("testdata/opensky_states.json")
	require.NoError(t, err, "captured OpenSky response missing")
	states, err := adsb.DecodeStates(body)
	require.NoError(t, err)
	return states
}

func TestDecodeRealOpenSkyResponse(t *testing.T) {
	t.Parallel()
	states := loadFixture(t)
	require.NotEmpty(t, states)

	for _, s := range states {
		assert.NotEmpty(t, s.ICAO24)
		assert.True(t, s.HasPosition)
		assert.NotEqual(t, "none", s.AltitudeSource)
	}
}

func TestDecodeDistinguishesNullFromZero(t *testing.T) {
	t.Parallel()

	// OpenSky uses null liberally, and most aircraft in a live sample had no
	// geometric altitude. Treating null as zero would put an airliner at sea
	// level and produce a wildly wrong slant range.
	body := []byte(`{"time":1,"states":[["abc123","TEST    ","AU",1,1,150.0,-34.0,null,false,120.0,90.0,null,null,null,null,false,0,0]]}`)
	states, err := adsb.DecodeStates(body)
	require.NoError(t, err)
	require.Len(t, states, 1)
	assert.Equal(t, "none", states[0].AltitudeSource, "no altitude at all must be recognised as such")
}

func TestDecodePrefersGeometricAltitude(t *testing.T) {
	t.Parallel()

	// Barometric altitude is pressure-derived and drifts; geometric is preferred
	// where present, and which one was used is recorded so a questionable match
	// can be judged later.
	body := []byte(`{"time":1,"states":[["abc123","TEST    ","AU",1,1,150.0,-34.0,1150.0,false,120.0,90.0,null,null,1280.0,null,false,0,0]]}`)
	states, err := adsb.DecodeStates(body)
	require.NoError(t, err)
	require.Len(t, states, 1)
	assert.InDelta(t, 1280.0, states[0].AltitudeM(), 0.01)
	assert.Equal(t, "geometric", states[0].AltitudeSource)
}

func TestResolveRequiresStation(t *testing.T) {
	t.Parallel()
	p := &adsb.Provider{Source: &fixtureSource{}, Config: adsb.DefaultConfig()}

	// No station means no geometry. This must read as a configuration problem,
	// not as a quiet sky, or a broken setup looks like a site with no traffic.
	_, err := p.Resolve(t.Context(), &enrichment.Request{Domain: "aircraft"})
	require.ErrorIs(t, err, enrichment.ErrNotConfigured)
}

func TestResolveOnlyServesAircraft(t *testing.T) {
	t.Parallel()
	p := &adsb.Provider{Source: &fixtureSource{}, Config: adsb.DefaultConfig()}
	assert.Equal(t, []string{"aircraft"}, p.Domains())
	assert.Equal(t, "adsb", p.Name())
}

func TestResolveMatchesOverheadAircraft(t *testing.T) {
	t.Parallel()

	// One aircraft, low and almost directly overhead.
	src := &fixtureSource{states: []adsb.State{{
		ICAO24: "7c7801", Callsign: "QFA557", HasPosition: true,
		Latitude: station.Latitude + 0.002, Longitude: station.Longitude,
		GeoAltitudeM: 1200, VelocityMS: 124, TrackDeg: 167, AltitudeSource: "geometric",
	}}}
	p := &adsb.Provider{Source: src, Config: adsb.DefaultConfig()}

	got, err := p.Resolve(t.Context(), &enrichment.Request{Domain: "aircraft", Station: station})
	require.NoError(t, err)
	assert.Equal(t, "adsb", got.Provider)
	assert.Equal(t, "opensky", got.Source)
	assert.Equal(t, "QFA557", got.Attributes["callsign"])
	assert.Equal(t, "7c7801", got.Attributes["hex"])
	assert.Positive(t, got.LagCorrectionMs, "the lag correction must be applied and recorded")
	assert.Equal(t, "geometric", got.Attributes["altitude_source"])
}

func TestResolveRejectsDistantAircraft(t *testing.T) {
	t.Parallel()

	// The fixture's real aircraft are 20km+ from the station - beyond credible
	// audibility, and beyond where back-projection can be trusted. The honest
	// answer is no match, not the nearest thing in the sky.
	p := &adsb.Provider{Source: &fixtureSource{states: loadFixture(t)}, Config: adsb.DefaultConfig()}

	_, err := p.Resolve(t.Context(), &enrichment.Request{Domain: "aircraft", Station: station})
	require.ErrorIs(t, err, enrichment.ErrNoMatch)
}

func TestResolveIgnoresGroundTraffic(t *testing.T) {
	t.Parallel()

	src := &fixtureSource{states: []adsb.State{{
		ICAO24: "grnd01", Callsign: "TAXI", HasPosition: true, OnGround: true,
		Latitude: station.Latitude, Longitude: station.Longitude,
		GeoAltitudeM: 141, AltitudeSource: "geometric",
	}}}
	p := &adsb.Provider{Source: src, Config: adsb.DefaultConfig()}

	_, err := p.Resolve(t.Context(), &enrichment.Request{Domain: "aircraft", Station: station})
	require.ErrorIs(t, err, enrichment.ErrNoMatch, "an aircraft on the ground cannot be the source of an overflight")
}

func TestResolveDeclinesWhenTwoAircraftAreEquallyPlausible(t *testing.T) {
	t.Parallel()

	// Two aircraft in near-identical positions. Naming one would be a coin toss
	// presented as a fact, and downstream a wrong identity is indistinguishable
	// from a right one - so neither is returned.
	src := &fixtureSource{states: []adsb.State{
		{
			ICAO24: "aaa111", Callsign: "ONE", HasPosition: true,
			Latitude: station.Latitude + 0.002, Longitude: station.Longitude,
			GeoAltitudeM: 1200, VelocityMS: 120, TrackDeg: 90, AltitudeSource: "geometric",
		},
		{
			ICAO24: "bbb222", Callsign: "TWO", HasPosition: true,
			Latitude: station.Latitude + 0.0021, Longitude: station.Longitude + 0.0001,
			GeoAltitudeM: 1201, VelocityMS: 120, TrackDeg: 90, AltitudeSource: "geometric",
		},
	}}
	p := &adsb.Provider{Source: src, Config: adsb.DefaultConfig()}

	_, err := p.Resolve(t.Context(), &enrichment.Request{Domain: "aircraft", Station: station})
	require.ErrorIs(t, err, enrichment.ErrNoMatch, "an ambiguous sky must yield no identity rather than a guess")
}

func TestResolveConfidenceIsHigherForALoneAircraft(t *testing.T) {
	t.Parallel()
	overhead := adsb.State{
		ICAO24: "aaa111", Callsign: "ONE", HasPosition: true,
		Latitude: station.Latitude + 0.002, Longitude: station.Longitude,
		GeoAltitudeM: 1200, VelocityMS: 120, TrackDeg: 90, AltitudeSource: "geometric",
	}
	// A second aircraft close enough to be a real alternative, but clearly
	// worse than the overhead one - so a match is still returned, with less
	// confidence. A distant distractor would not lower confidence at all, and
	// correctly so: it never competed.
	distractor := adsb.State{
		ICAO24: "bbb222", Callsign: "TWO", HasPosition: true,
		Latitude: station.Latitude + 0.012, Longitude: station.Longitude,
		GeoAltitudeM: 2140, VelocityMS: 120, TrackDeg: 90, AltitudeSource: "geometric",
	}

	alone := &adsb.Provider{Source: &fixtureSource{states: []adsb.State{overhead}}, Config: adsb.DefaultConfig()}
	crowded := &adsb.Provider{Source: &fixtureSource{states: []adsb.State{overhead, distractor}}, Config: adsb.DefaultConfig()}

	gotAlone, err := alone.Resolve(t.Context(), &enrichment.Request{Domain: "aircraft", Station: station})
	require.NoError(t, err)
	gotCrowded, err := crowded.Resolve(t.Context(), &enrichment.Request{Domain: "aircraft", Station: station})
	require.NoError(t, err)

	assert.Equal(t, gotAlone.Attributes["hex"], gotCrowded.Attributes["hex"], "the same aircraft should win")
	assert.Greater(t, gotAlone.Confidence, gotCrowded.Confidence,
		"a lone aircraft in an empty sky is a safer call than the best of several")
}

func TestResolveSurfacesSourceErrors(t *testing.T) {
	t.Parallel()
	src := &fixtureSource{err: errors.New("network unreachable")}
	p := &adsb.Provider{Source: src, Config: adsb.DefaultConfig()}

	_, err := p.Resolve(t.Context(), &enrichment.Request{Domain: "aircraft", Station: station})
	require.Error(t, err)
	assert.NotErrorIs(t, err, enrichment.ErrNoMatch, "a broken source must not look like an empty sky")
}

// --- client behaviour ---

func TestClientParsesRateLimitHeaders(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			_, _ = w.Write([]byte(`{"access_token":"t","expires_in":1800}`))
			return
		}
		w.Header().Set("X-Rate-Limit-Remaining", "3997")
		_, _ = w.Write([]byte(`{"time":1,"states":[]}`))
	}))
	defer srv.Close()

	c := adsb.NewOpenSkyClient("id", "secret")
	c.BaseURL, c.TokenURL = srv.URL, srv.URL+"/token"

	_, err := c.StatesInBox(t.Context(), -35, 150, -33, 152)
	require.NoError(t, err)

	credits, known := c.CreditsRemaining()
	assert.True(t, known)
	assert.Equal(t, 3997, credits)
}

func TestClientWithholdsRequestAtCreditFloor(t *testing.T) {
	t.Parallel()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			_, _ = w.Write([]byte(`{"access_token":"t","expires_in":1800}`))
			return
		}
		calls++
		w.Header().Set("X-Rate-Limit-Remaining", "150")
		_, _ = w.Write([]byte(`{"time":1,"states":[]}`))
	}))
	defer srv.Close()

	c := adsb.NewOpenSkyClient("id", "secret")
	c.BaseURL, c.TokenURL = srv.URL, srv.URL+"/token"
	c.CreditFloor = 200
	// Reuse disabled so this tests the reserve and nothing else. With the cache
	// on, the second call would be answered from the first fetch - correctly,
	// since that costs no credits - and the floor would never be reached.
	c.StateTTL = -1

	// First call succeeds and learns the balance is below the floor.
	_, err := c.StatesInBox(t.Context(), -35, 150, -33, 152)
	require.NoError(t, err)

	// The reserve exists so the M6 collector cannot exhaust the shared daily
	// allowance and leave real detections unidentifiable for the rest of the day.
	_, err = c.StatesInBox(t.Context(), -35, 150, -33, 152)
	require.ErrorIs(t, err, adsb.ErrCreditFloor)
	assert.Equal(t, 1, calls, "no request should reach the API once the reserve is hit")
}

func TestClientReportsRateLimiting(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			_, _ = w.Write([]byte(`{"access_token":"t","expires_in":1800}`))
			return
		}
		w.Header().Set("X-Rate-Limit-Retry-After-Seconds", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := adsb.NewOpenSkyClient("id", "secret")
	c.BaseURL, c.TokenURL = srv.URL, srv.URL+"/token"

	_, err := c.StatesInBox(t.Context(), -35, 150, -33, 152)
	require.ErrorIs(t, err, adsb.ErrRateLimited)

	// The server's retry-after must be honoured rather than hammered through.
	_, err = c.StatesInBox(t.Context(), -35, 150, -33, 152)
	require.ErrorIs(t, err, adsb.ErrRateLimited)
}

func TestClientRequiresCredentials(t *testing.T) {
	t.Parallel()
	c := adsb.NewOpenSkyClient("", "")
	_, err := c.StatesInBox(t.Context(), -35, 150, -33, 152)
	// Anonymous access ignores the time parameter, so the acoustic-lag
	// correction - the entire point - is impossible without credentials.
	require.ErrorContains(t, err, "credentials")
}

// The provider wrote "opensky" into every identification as a literal, which
// became false the moment a second source could answer. Two networks see
// different aircraft with different latency, and a match that looks wrong later
// cannot be judged without knowing which one said it.
func TestResolveRecordsWhichSourceActuallyAnswered(t *testing.T) {
	t.Parallel()

	src := &fixtureSource{states: []adsb.State{{
		ICAO24: "7c74ce", Callsign: "XCW", HasPosition: true,
		Latitude: station.Latitude + 0.002, Longitude: station.Longitude,
		GeoAltitudeM: 340, VelocityMS: 50, TrackDeg: 121, AltitudeSource: "geometric",
		Source: "adsb.lol",
	}}}
	p := &adsb.Provider{Source: src, Config: adsb.DefaultConfig()}

	got, err := p.Resolve(t.Context(), &enrichment.Request{Domain: "aircraft", Station: station})
	require.NoError(t, err)
	assert.Equal(t, "adsb.lol", got.Source,
		"an identification made from adsb.lol data must not be stored as OpenSky's")
}
