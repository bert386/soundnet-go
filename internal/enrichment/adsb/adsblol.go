package adsb

// A second source of aircraft positions, for when the first one runs out.
//
// OpenSky meters access: 4000 credits a day for an authenticated account, one
// per query of a box this size. On the first night the station spent the lot in
// eight hours and spent the rest of the morning unable to identify anything -
// which cost more than identifications, because the domain correction and the
// pass grouping are both built on top of them.
//
// adsb.lol is the same data from a different volunteer network, with no key and
// no daily ceiling. It also answers with more than OpenSky does: registration
// and ICAO type arrive in the same response, where OpenSky needs a separate
// third-party lookup for them.
//
// Two things to keep in view. Their documentation says an API key will
// eventually be required, obtainable by feeding data back; and their rate
// limits are "dynamic based on environment load", which is an absence of a
// documented ceiling rather than a promise of none. So this is the fallback and
// OpenSky stays the primary: a metered source with a known limit is a better
// thing to depend on than an unmetered one with an unknown one.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bert386/soundnet-go/internal/enrichment"
)

// DefaultADSBLolBaseURL is the public endpoint.
const DefaultADSBLolBaseURL = "https://api.adsb.lol"

// Unit conversions. The readsb JSON these services emit is in aviation units -
// feet and knots - while everything downstream of StateSource is metres and
// metres per second. Getting this wrong would not fail loudly: it would quietly
// place every aircraft at three times its real altitude and match the wrong
// one, so the conversion lives in named constants rather than inline.
const (
	feetToMetres   = 0.3048
	knotsToMetresS = 0.514444

	// nauticalMile is the unit the point endpoint takes its radius in.
	metresPerNauticalMile = 1852.0

	// maxRadiusNM is the endpoint's documented ceiling.
	maxRadiusNM = 250
)

// userAgent identifies this station to the service.
//
// Not decoration. Their documentation asks for responsible use and says a 4xx
// means you are doing something wrong, and Go's default "Go-http-client/2.0"
// tells an operator nothing about who is calling or why. Saying so is the least
// a free service is owed.
const userAgent = "SoundNet/1.0 (BirdNET-Go fork; acoustic event station)"

// ADSBLolClient reads aircraft states from adsb.lol.
type ADSBLolClient struct {
	HTTP    *http.Client
	BaseURL string

	// StateTTL is how long a fetched sky is reused for. Zero uses the default,
	// negative disables reuse.
	//
	// This exists because leaving it out broke the fallback within minutes of
	// it going live. The reuse window on the OpenSky client is inside that
	// client, so the backup inherited none of it and took every enrichment
	// query raw - several a second during a busy pass - and adsb.lol rate
	// limited the station immediately, entirely fairly. A single curl had
	// worked perfectly, which is exactly why one curl is not a test.
	StateTTL time.Duration

	mu           sync.Mutex
	cachedStates []State
	cachedBox    [4]float64
	cachedAt     time.Time
	now          func() time.Time
}

// MaxStaleAge is how old a cached sky may be and still stand in for one the
// service refused.
//
// adsb.lol rate limits probabilistically rather than by a quota: measured at
// the station, seven requests in ten succeeded at a ten-second interval, with
// no Retry-After to wait for. Failing those three outright would lose an
// identification for no reason, when a sky from twenty seconds ago is sitting
// in memory.
//
// Thirty seconds because the states are dead reckoned forward to now before
// being returned, and constant track and speed stop being a fair assumption
// somewhere past that. It is the same assumption, over the same sort of
// interval, that the acoustic-lag correction already makes in the other
// direction - and LagIsCredible caps that at twenty seconds for the same
// reason.
const MaxStaleAge = 30 * time.Second

// DefaultADSBLolStateTTL is the default reuse window.
//
// Longer than OpenSky's five seconds on purpose. There the window is the
// service's own update interval, so reusing within it cannot return anything a
// fresh call would not. Here it is a courtesy to an unmetered service: ten
// seconds bounds the station to six requests a minute however loud the sky
// gets, and an aircraft's position ten seconds stale is well inside what the
// acoustic-lag correction already back-projects through.
const DefaultADSBLolStateTTL = 10 * time.Second

// NewADSBLolClient returns a client against the public endpoint.
func NewADSBLolClient() *ADSBLolClient {
	return &ADSBLolClient{
		HTTP:     &http.Client{Timeout: 15 * time.Second},
		BaseURL:  DefaultADSBLolBaseURL,
		StateTTL: DefaultADSBLolStateTTL,
	}
}

// SetClock replaces the time source, so the reuse window can be exercised
// without sleeping.
func (c *ADSBLolClient) SetClock(now func() time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

func (c *ADSBLolClient) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// cachedFor returns a still-valid cached sky for this box, if there is one.
func (c *ADSBLolClient) cachedFor(box [4]float64) ([]State, bool) {
	ttl := c.StateTTL
	switch {
	case ttl < 0:
		return nil, false
	case ttl == 0:
		ttl = DefaultADSBLolStateTTL
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cachedAt.IsZero() || c.cachedBox != box {
		return nil, false
	}
	if c.clock().Sub(c.cachedAt) >= ttl {
		return nil, false
	}
	out := make([]State, len(c.cachedStates))
	copy(out, c.cachedStates)
	return out, true
}

// staleFor returns a cached sky older than the reuse window but young enough to
// stand in, with every aircraft advanced along its track to now.
//
// Returned only to a caller that has just been refused a fresh one. Dead
// reckoning rather than the stored positions: at 180 m/s an aircraft covers
// five kilometres in thirty seconds, which is most of the match radius, so
// serving the stored position would not be stale data - it would be wrong data.
func (c *ADSBLolClient) staleFor(box [4]float64) ([]State, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cachedAt.IsZero() || c.cachedBox != box || len(c.cachedStates) == 0 {
		return nil, false
	}
	age := c.clock().Sub(c.cachedAt)
	if age <= 0 || age > MaxStaleAge {
		return nil, false
	}

	out := make([]State, 0, len(c.cachedStates))
	for i := range c.cachedStates {
		s := c.cachedStates[i]
		if s.HasPosition && s.VelocityMS > 0 {
			moved := enrichment.Project(enrichment.Position{
				Latitude:      s.Latitude,
				Longitude:     s.Longitude,
				AltitudeM:     s.AltitudeM(),
				GroundSpeedMS: s.VelocityMS,
				TrackDeg:      s.TrackDeg,
			}, age)
			s.Latitude, s.Longitude = moved.Latitude, moved.Longitude
		}
		out = append(out, s)
	}
	return out, true
}

// cache stores a freshly fetched sky.
func (c *ADSBLolClient) cache(box [4]float64, states []State) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cachedBox = box
	c.cachedAt = c.clock()
	c.cachedStates = make([]State, len(states))
	copy(c.cachedStates, states)
}

// adsbLolSourceName is what adsb.lol is called in logs and stored provenance.
const adsbLolSourceName = "adsb.lol"

// Name identifies the source in logs.
func (c *ADSBLolClient) Name() string { return adsbLolSourceName }

// CreditsRemaining reports that this source does not meter access.
//
// Unknown rather than a large number, deliberately. A number would be a claim
// about a ceiling nobody has documented, and the callers that consult this use
// it to decide when to stop - which, against an unmetered source, is never.
func (c *ADSBLolClient) CreditsRemaining() (credits int, known bool) { return 0, false }

// adsbLolResponse is the subset of the readsb-style document that matters here.
type adsbLolAircraft struct {
	Hex    string  `json:"hex"`
	Flight string  `json:"flight"`
	Reg    string  `json:"r"`
	Type   string  `json:"t"`
	Lat    float64 `json:"lat"`
	Lon    float64 `json:"lon"`
	GS     float64 `json:"gs"`
	Track  float64 `json:"track"`

	// AltBaro is a number of feet, or the string "ground". A single JSON field
	// with two types is why this is json.RawMessage and not a float: decoding
	// it as a number makes every aircraft on the ground a parse error, and
	// skipping parse errors would silently drop them into the airborne set.
	AltBaro json.RawMessage `json:"alt_baro"`
	AltGeom *float64        `json:"alt_geom"`

	hasPosition bool
}

type adsbLolResponse struct {
	Aircraft []adsbLolAircraft `json:"ac"`
}

// StatesInBox satisfies StateSource.
//
// adsb.lol answers about a point and a radius rather than a box, so the box is
// converted to the smallest circle containing it and the results are filtered
// back down. Filtering rather than trusting the circle keeps this source's
// answer identical in shape to OpenSky's, which is what lets either of them
// stand in for the other without the provider knowing which it got.
func (c *ADSBLolClient) StatesInBox(ctx context.Context, latMin, lonMin, latMax, lonMax float64) ([]State, error) {
	box := [4]float64{latMin, lonMin, latMax, lonMax}
	if states, ok := c.cachedFor(box); ok {
		return states, nil
	}

	centreLat := (latMin + latMax) / 2
	centreLon := (lonMin + lonMax) / 2
	radius := coveringRadiusNM(latMin, lonMin, latMax, lonMax)

	base := strings.TrimSuffix(c.BaseURL, "/")
	if base == "" {
		base = DefaultADSBLolBaseURL
	}
	url := fmt.Sprintf("%s/v2/point/%.5f/%.5f/%d", base, centreLat, centreLon, radius)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("adsb.lol: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("adsb.lol: fetch states: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests {
		// A recent sky is better than none, and costs them nothing. Retrying
		// instead would be the one thing a service that has just refused you is
		// entitled not to expect.
		if states, ok := c.staleFor(box); ok {
			return states, nil
		}
		// Their limit is dynamic and undocumented, so this is the only way it
		// announces itself. Reported as the same condition OpenSky reports, so
		// a caller chaining the two does not need to know whose limit it was.
		return nil, ErrCreditFloor
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("adsb.lol: unexpected status %d", resp.StatusCode)
	}

	var decoded adsbLolResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("adsb.lol: decode states: %w", err)
	}

	out := make([]State, 0, len(decoded.Aircraft))
	for i := range decoded.Aircraft {
		state := decoded.Aircraft[i].toState()
		if state.HasPosition &&
			(state.Latitude < latMin || state.Latitude > latMax ||
				state.Longitude < lonMin || state.Longitude > lonMax) {
			continue
		}
		out = append(out, state)
	}
	c.cache(box, out)
	return out, nil
}

// toState converts one aircraft into the shape the provider reasons about.
func (a *adsbLolAircraft) toState() State {
	state := State{
		ICAO24:      strings.ToLower(strings.TrimSpace(a.Hex)),
		Callsign:    strings.TrimSpace(a.Flight),
		Latitude:    a.Lat,
		Longitude:   a.Lon,
		VelocityMS:  a.GS * knotsToMetresS,
		TrackDeg:    a.Track,
		HasPosition: a.Lat != 0 || a.Lon != 0,
		Source:      adsbLolSourceName,
	}

	baroFeet, onGround := decodeAltBaro(a.AltBaro)
	state.OnGround = onGround
	switch {
	case a.AltGeom != nil:
		state.GeoAltitudeM = *a.AltGeom * feetToMetres
		state.AltitudeSource = "geometric"
		if baroFeet != nil {
			state.BaroAltitudeM = *baroFeet * feetToMetres
		}
	case baroFeet != nil:
		state.BaroAltitudeM = *baroFeet * feetToMetres
		state.AltitudeSource = "barometric"
	default:
		// No usable altitude. Named rather than left as the zero value, because
		// the provider skips on exactly this and a silent zero would place the
		// aircraft at sea level instead.
		state.AltitudeSource = "none"
	}
	return state
}

// decodeAltBaro reads the field that is a number of feet or the word "ground".
func decodeAltBaro(raw json.RawMessage) (feet *float64, onGround bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var asNumber float64
	if err := json.Unmarshal(raw, &asNumber); err == nil {
		return &asNumber, false
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return nil, strings.EqualFold(strings.TrimSpace(asString), "ground")
	}
	return nil, false
}

// coveringRadiusNM returns a radius, in whole nautical miles, of a circle about
// the box's centre that contains the whole box.
//
// Rounded up rather than down: a circle that does not quite cover the box would
// drop aircraft near its corners, and since the results are filtered back to
// the box afterwards, asking for slightly too much costs nothing but a few
// discarded rows.
func coveringRadiusNM(latMin, lonMin, latMax, lonMax float64) int {
	const metresPerDegreeLat = 111_320.0

	centreLat := (latMin + latMax) / 2
	latSpanM := (latMax - latMin) / 2 * metresPerDegreeLat
	lonSpanM := (lonMax - lonMin) / 2 * metresPerDegreeLat * math.Cos(centreLat*math.Pi/180)

	diagonal := math.Hypot(latSpanM, lonSpanM)
	radius := int(math.Ceil(diagonal / metresPerNauticalMile))
	return min(max(radius, 1), maxRadiusNM)
}
