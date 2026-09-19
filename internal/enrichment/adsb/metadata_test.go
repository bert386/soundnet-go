package adsb_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bert386/soundnet-go/internal/enrichment"
	"github.com/bert386/soundnet-go/internal/enrichment/adsb"
)

// adsbdbStub serves the captured responses and counts requests, so caching can
// be asserted rather than assumed.
func adsbdbStub(t *testing.T, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	aircraft, err := os.ReadFile("testdata/adsbdb_aircraft.json")
	require.NoError(t, err)
	route, err := os.ReadFile("testdata/adsbdb_route.json")
	require.NoError(t, err)

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/aircraft/7c7801":
			_, _ = w.Write(aircraft)
		case "/callsign/QFA557":
			_, _ = w.Write(route)
		default:
			// Exactly what the real service returns for an unknown identifier.
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"response":"unknown aircraft"}`))
		}
	}))
}

func TestAircraftLookupDecodesRealResponse(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	srv := adsbdbStub(t, &hits)
	defer srv.Close()

	r := adsb.NewAdsbdbResolver()
	r.BaseURL = srv.URL

	got, err := r.Aircraft(t.Context(), "7c7801")
	require.NoError(t, err)
	assert.Equal(t, "VH-XZN", got.Registration)
	assert.Equal(t, "B738", got.TypeCode)
	assert.Equal(t, "Boeing", got.Manufacturer)
	assert.Equal(t, "Qantas", got.Operator)
}

func TestRouteLookupGivesFlightNumberAndAirports(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	srv := adsbdbStub(t, &hits)
	defer srv.Close()

	r := adsb.NewAdsbdbResolver()
	r.BaseURL = srv.URL

	got, err := r.Route(t.Context(), "QFA557")
	require.NoError(t, err)
	assert.Equal(t, "QF557", got.FlightIATA, "the number a passenger would recognise")
	assert.Equal(t, "QFA557", got.FlightICAO)
	assert.Equal(t, "BNE", got.OriginIATA)
	assert.Equal(t, "SYD", got.DestinationIATA)
	assert.Equal(t, "Brisbane", got.OriginName)
}

func TestLookupsAreCached(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	srv := adsbdbStub(t, &hits)
	defer srv.Close()

	r := adsb.NewAdsbdbResolver()
	r.BaseURL = srv.URL

	for range 5 {
		_, err := r.Aircraft(t.Context(), "7c7801")
		require.NoError(t, err)
	}
	// A registration does not change, and this is a free community service.
	// Repeated overflights of the same aircraft must not re-query it.
	assert.Equal(t, int32(1), hits.Load(), "aircraft lookups must be cached")
}

func TestUnknownIdentifierIsCachedNegatively(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	srv := adsbdbStub(t, &hits)
	defer srv.Close()

	r := adsb.NewAdsbdbResolver()
	r.BaseURL = srv.URL

	for range 4 {
		_, err := r.Aircraft(t.Context(), "ffffff")
		require.ErrorIs(t, err, adsb.ErrMetadataNotFound)
	}
	// Military, private and newly registered aircraft are routinely absent.
	// Asking repeatedly for an answer known not to exist is just rude.
	assert.Equal(t, int32(1), hits.Load(), "a known-absent identifier must not be re-queried")
}

// failingResolver stands in for adsbdb being unreachable.
type failingResolver struct{ calls atomic.Int32 }

func (f *failingResolver) Aircraft(context.Context, string) (*adsb.AircraftInfo, error) {
	f.calls.Add(1)
	return nil, errors.New("adsbdb unreachable")
}
func (f *failingResolver) Route(context.Context, string) (*adsb.RouteInfo, error) {
	f.calls.Add(1)
	return nil, errors.New("adsbdb unreachable")
}

func TestIdentificationSurvivesMetadataFailure(t *testing.T) {
	t.Parallel()
	src := &fixtureSource{states: []adsb.State{{
		ICAO24: "7c7801", Callsign: "QFA557", HasPosition: true,
		Latitude: station.Latitude + 0.002, Longitude: station.Longitude,
		GeoAltitudeM: 1200, VelocityMS: 124, TrackDeg: 167, AltitudeSource: "geometric",
	}}}
	failing := &failingResolver{}
	p := &adsb.Provider{Source: src, Config: adsb.DefaultConfig(), Metadata: failing}

	got, err := p.Resolve(t.Context(), &enrichment.Request{Domain: "aircraft", Station: station})

	// The identification is already made from broadcast data and is the valuable
	// part. Losing it because a free third-party API was briefly down would be a
	// poor trade.
	require.NoError(t, err)
	assert.Equal(t, "7c7801", got.Attributes["hex"])
	assert.Equal(t, "QFA557", got.Attributes["callsign"])
	assert.Positive(t, failing.calls.Load(), "the lookup was attempted")
	assert.NotContains(t, got.Attributes, "registration", "a failed lookup must add nothing, not an empty value")
	assert.NotContains(t, got.Attributes, "metadata_source")
}

func TestFullIdentityIncludesTypeRegistrationAndRoute(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	srv := adsbdbStub(t, &hits)
	defer srv.Close()

	meta := adsb.NewAdsbdbResolver()
	meta.BaseURL = srv.URL

	src := &fixtureSource{states: []adsb.State{{
		ICAO24: "7c7801", Callsign: "QFA557", HasPosition: true,
		Latitude: station.Latitude + 0.002, Longitude: station.Longitude,
		GeoAltitudeM: 1200, VelocityMS: 124, TrackDeg: 167, AltitudeSource: "geometric",
	}}}
	p := &adsb.Provider{Source: src, Config: adsb.DefaultConfig(), Metadata: meta}

	got, err := p.Resolve(t.Context(), &enrichment.Request{Domain: "aircraft", Station: station})
	require.NoError(t, err)

	// The complete picture an operator wants to see next to a detection.
	assert.Equal(t, "7c7801", got.Attributes["hex"])
	assert.Equal(t, "VH-XZN", got.Attributes["registration"])
	assert.Equal(t, "B738", got.Attributes["type_code"])
	assert.Equal(t, "Qantas", got.Attributes["operator"])
	assert.Equal(t, "QF557", got.Attributes["flight_iata"])
	assert.Equal(t, "BNE", got.Attributes["origin_iata"])
	assert.Equal(t, "SYD", got.Attributes["destination_iata"])

	// Provenance is recorded: the hex and callsign were broadcast by the
	// aircraft, the rest was looked up in someone else's database.
	assert.Equal(t, "broadcast", got.Attributes["identity_source"])
	assert.Equal(t, "adsbdb", got.Attributes["metadata_source"])
}

func TestMetadataIsOptional(t *testing.T) {
	t.Parallel()
	src := &fixtureSource{states: []adsb.State{{
		ICAO24: "7c7801", Callsign: "QFA557", HasPosition: true,
		Latitude: station.Latitude + 0.002, Longitude: station.Longitude,
		GeoAltitudeM: 1200, VelocityMS: 124, TrackDeg: 167, AltitudeSource: "geometric",
	}}}
	// No resolver configured: the third-party lookup is opt-in, so nothing
	// should reach out and the core identity still resolves.
	p := &adsb.Provider{Source: src, Config: adsb.DefaultConfig()}

	got, err := p.Resolve(t.Context(), &enrichment.Request{Domain: "aircraft", Station: station})
	require.NoError(t, err)
	assert.Equal(t, "7c7801", got.Attributes["hex"])
	assert.NotContains(t, got.Attributes, "registration")
}
