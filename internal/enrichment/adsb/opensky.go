// Package adsb resolves aircraft identity from ADS-B data.
//
// It is the one enrichment provider with a genuinely authoritative source: an
// aircraft broadcasts its own identity, so a match is a fact rather than an
// inference. Everything hard about this package is in deciding *which* aircraft
// the microphone heard, not in what that aircraft is.
package adsb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// State is one aircraft's reported state, decoded from OpenSky's positional
// array format into something with names.
type State struct {
	ICAO24        string
	Callsign      string
	Longitude     float64
	Latitude      float64
	BaroAltitudeM float64
	GeoAltitudeM  float64
	VelocityMS    float64
	TrackDeg      float64
	OnGround      bool
	HasPosition   bool

	// AltitudeSource records which altitude field was used. Geometric altitude
	// is preferred but frequently absent - in a live sample most aircraft had
	// only barometric - and barometric is pressure-derived, so it drifts from
	// true height. Recording the source lets a questionable match be judged
	// later rather than silently trusted.
	AltitudeSource string
}

// AltitudeM returns the best available altitude, preferring geometric.
func (s *State) AltitudeM() float64 {
	if s.GeoAltitudeM != 0 {
		return s.GeoAltitudeM
	}
	return s.BaroAltitudeM
}

// StateSource supplies aircraft states for a bounding box. The provider depends
// on this rather than on the HTTP client directly, so tests run against captured
// fixtures with no network - which TESTING.md requires.
type StateSource interface {
	StatesInBox(ctx context.Context, latMin, lonMin, latMax, lonMax float64) ([]State, error)
}

// OpenSkyClient talks to the OpenSky Network REST API.
type OpenSkyClient struct {
	ClientID     string
	ClientSecret string
	HTTP         *http.Client
	BaseURL      string
	TokenURL     string

	// CreditFloor is the number of API credits held in reserve. The daily
	// allowance is shared between runtime enrichment and the M6 collector, and
	// without a floor a busy collector would exhaust the budget and leave real
	// detections unidentifiable for the rest of the day.
	CreditFloor int

	// StateTTL is how long a fetched sky is reused for.
	//
	// /states/all has no time parameter - it returns the current sky and costs a
	// credit each time - while OpenSky itself updates an authenticated feed
	// roughly every five seconds. So two detections seconds apart are billed
	// twice for data that did not change, which was already wasteful and became
	// material once ambiguous vehicle labels started asking as well.
	//
	// Short by design. The lag correction back-projects an aircraft's track from
	// its reported position, and that projection is only trustworthy over a few
	// seconds; a long TTL would quietly turn an accurate match into a stale one.
	// Zero uses DefaultStateTTL, negative disables reuse entirely.
	StateTTL time.Duration

	mu             sync.Mutex
	now            func() time.Time
	token          string
	tokenExpiry    time.Time
	creditsLeft    int
	creditsKnown   bool
	retryAfterTime time.Time
	cachedStates   []State
	cachedBox      [4]float64
	cachedAt       time.Time
}

// SetClock replaces the client's time source. For tests only: it exists so the
// reuse window can be exercised without sleeping, which is what kept a five
// second TTL from costing five seconds of test time.
func (c *OpenSkyClient) SetClock(now func() time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

// DefaultStateTTL is the default reuse window for a fetched sky. Five seconds is
// OpenSky's own authenticated update interval: reusing within it cannot return
// anything the API would not have returned again.
const DefaultStateTTL = 5 * time.Second

// clock returns the client's time source. Callers hold mu: now is written by
// SetClock, so reading it unguarded would be a race the detector rightly
// refuses.
func (c *OpenSkyClient) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// cachedFor returns a still-valid cached sky for this box, if there is one.
func (c *OpenSkyClient) cachedFor(box [4]float64) ([]State, bool) {
	ttl := c.StateTTL
	switch {
	case ttl < 0:
		return nil, false
	case ttl == 0:
		ttl = DefaultStateTTL
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cachedAt.IsZero() || c.cachedBox != box {
		return nil, false
	}
	if c.clock().Sub(c.cachedAt) >= ttl {
		return nil, false
	}
	// Copied: the caller ranges over this and the next caller gets the same
	// backing array, so handing out the slice itself would let one request's
	// reader see another's mutation.
	out := make([]State, len(c.cachedStates))
	copy(out, c.cachedStates)
	return out, true
}

// cache stores a freshly fetched sky.
func (c *OpenSkyClient) cache(box [4]float64, states []State) {
	if c.StateTTL < 0 {
		return
	}
	stored := make([]State, len(states))
	copy(stored, states)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cachedStates = stored
	c.cachedBox = box
	c.cachedAt = c.clock()
}

// Default endpoints.
const (
	DefaultBaseURL  = "https://opensky-network.org/api"
	DefaultTokenURL = "https://auth.opensky-network.org/auth/realms/opensky-network/protocol/openid-connect/token"
)

// ErrCreditFloor means the request was not sent because doing so would breach
// the reserve. Distinct from a rate-limit rejection: nothing was spent, and the
// caller may retry once the daily allowance refills.
var ErrCreditFloor = errors.New("adsb: api credit reserve reached, request withheld")

// ErrRateLimited means the API refused the request. RetryAfter says when the
// server indicated it would accept another.
var ErrRateLimited = errors.New("adsb: rate limited by OpenSky")

// NewOpenSkyClient returns a client with sensible defaults.
//
// Authentication is mandatory, not merely preferred. Anonymous access ignores
// the time parameter and only ever returns the current sky, which makes the
// acoustic-lag correction impossible - and that correction is the whole point.
func NewOpenSkyClient(clientID, clientSecret string) *OpenSkyClient {
	return &OpenSkyClient{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		HTTP:         &http.Client{Timeout: 15 * time.Second},
		BaseURL:      DefaultBaseURL,
		TokenURL:     DefaultTokenURL,
		CreditFloor:  200,
	}
}

// CreditsRemaining reports the last known credit balance, and whether any
// response has reported one yet.
func (c *OpenSkyClient) CreditsRemaining() (credits int, known bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.creditsLeft, c.creditsKnown
}

// token acquisition. OpenSky moved to OAuth2 client credentials and no longer
// accepts basic auth, so a bearer token is obtained and cached until shortly
// before it expires.
func (c *OpenSkyClient) bearerToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.token != "" && time.Now().Before(c.tokenExpiry) {
		t := c.token
		c.mu.Unlock()
		return t, nil
	}
	c.mu.Unlock()

	if c.ClientID == "" || c.ClientSecret == "" {
		return "", fmt.Errorf("adsb: OpenSky client credentials are not configured")
	}

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("adsb: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("adsb: token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		// Deliberately does not echo the body: it can contain the client secret
		// in an error context, and this message may reach a support dump.
		return "", fmt.Errorf("adsb: token endpoint returned %s", resp.Status)
	}

	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("adsb: decode token response: %w", err)
	}
	if payload.AccessToken == "" {
		return "", fmt.Errorf("adsb: token endpoint returned no access token")
	}

	c.mu.Lock()
	c.token = payload.AccessToken
	// Refresh a minute early so a request never races the expiry.
	c.tokenExpiry = time.Now().Add(time.Duration(payload.ExpiresIn)*time.Second - time.Minute)
	c.mu.Unlock()
	return payload.AccessToken, nil
}

// StatesInBox returns aircraft within a bounding box.
//
// A small box is deliberate and not only about relevance: OpenSky charges by
// bounding-box area, and anything up to 25 square degrees costs a single credit,
// so a station-sized query is the cheapest possible request.
func (c *OpenSkyClient) StatesInBox(ctx context.Context, latMin, lonMin, latMax, lonMax float64) ([]State, error) {
	box := [4]float64{latMin, lonMin, latMax, lonMax}
	if states, ok := c.cachedFor(box); ok {
		return states, nil
	}

	c.mu.Lock()
	if !c.retryAfterTime.IsZero() && time.Now().Before(c.retryAfterTime) {
		retry := c.retryAfterTime
		c.mu.Unlock()
		return nil, fmt.Errorf("%w until %s", ErrRateLimited, retry.Format(time.RFC3339))
	}
	if c.creditsKnown && c.creditsLeft <= c.CreditFloor {
		left := c.creditsLeft
		c.mu.Unlock()
		return nil, fmt.Errorf("%w (%d remaining, floor %d)", ErrCreditFloor, left, c.CreditFloor)
	}
	c.mu.Unlock()

	token, err := c.bearerToken(ctx)
	if err != nil {
		return nil, err
	}

	q := url.Values{}
	q.Set("lamin", strconv.FormatFloat(latMin, 'f', 6, 64))
	q.Set("lomin", strconv.FormatFloat(lonMin, 'f', 6, 64))
	q.Set("lamax", strconv.FormatFloat(latMax, 'f', 6, 64))
	q.Set("lomax", strconv.FormatFloat(lonMax, 'f', 6, 64))
	q.Set("extended", "1")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/states/all?"+q.Encode(), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("adsb: build states request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("adsb: states request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	c.recordRateLimitHeaders(resp)

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, ErrRateLimited
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("adsb: states endpoint returned %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("adsb: read states response: %w", err)
	}
	states, err := DecodeStates(body)
	if err != nil {
		return nil, err
	}
	c.cache(box, states)
	return states, nil
}

// recordRateLimitHeaders keeps the client's view of the credit budget current.
func (c *OpenSkyClient) recordRateLimitHeaders(resp *http.Response) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v := resp.Header.Get("X-Rate-Limit-Remaining"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.creditsLeft = n
			c.creditsKnown = true
		}
	}
	if v := resp.Header.Get("X-Rate-Limit-Retry-After-Seconds"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.retryAfterTime = time.Now().Add(time.Duration(n) * time.Second)
		}
	}
}

// DecodeStates parses an OpenSky /states/all response.
//
// The wire format is an array of positional arrays rather than objects, so each
// field is addressed by index. The indices are fixed by the API and are named
// here rather than scattered as magic numbers.
func DecodeStates(body []byte) ([]State, error) {
	var payload struct {
		Time   int64               `json:"time"`
		States [][]json.RawMessage `json:"states"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("adsb: decode states: %w", err)
	}

	const (
		idxICAO24    = 0
		idxCallsign  = 1
		idxLongitude = 5
		idxLatitude  = 6
		idxBaroAlt   = 7
		idxOnGround  = 8
		idxVelocity  = 9
		idxTrack     = 10
		idxGeoAlt    = 13
	)

	out := make([]State, 0, len(payload.States))
	for _, row := range payload.States {
		if len(row) <= idxGeoAlt {
			// A short row is malformed rather than merely sparse; skipping it is
			// safer than indexing past the end for a single aircraft.
			continue
		}
		s := State{
			ICAO24:   strings.TrimSpace(jsonString(row[idxICAO24])),
			Callsign: strings.TrimSpace(jsonString(row[idxCallsign])),
			OnGround: jsonBool(row[idxOnGround]),
		}
		lon, lonOK := jsonFloat(row[idxLongitude])
		lat, latOK := jsonFloat(row[idxLatitude])
		s.HasPosition = lonOK && latOK
		s.Longitude, s.Latitude = lon, lat
		s.BaroAltitudeM, _ = jsonFloat(row[idxBaroAlt])
		geo, geoOK := jsonFloat(row[idxGeoAlt])
		s.GeoAltitudeM = geo
		s.VelocityMS, _ = jsonFloat(row[idxVelocity])
		s.TrackDeg, _ = jsonFloat(row[idxTrack])

		switch {
		case geoOK && geo != 0:
			s.AltitudeSource = "geometric"
		case s.BaroAltitudeM != 0:
			s.AltitudeSource = "barometric"
		default:
			s.AltitudeSource = "none"
		}
		out = append(out, s)
	}
	return out, nil
}

func jsonString(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

func jsonBool(raw json.RawMessage) bool {
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		return false
	}
	return b
}

// jsonFloat reports ok=false for JSON null, which OpenSky uses liberally: most
// aircraft in a live sample had no geometric altitude at all. Distinguishing
// null from zero matters, because zero is a real altitude.
func jsonFloat(raw json.RawMessage) (value float64, ok bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0, false
	}
	return f, true
}
