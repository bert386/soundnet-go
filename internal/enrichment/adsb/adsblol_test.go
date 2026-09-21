package adsb

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bert386/soundnet-go/internal/enrichment"
)

// serveFixture answers every request with a captured adsb.lol response, so the
// tests exercise the real document shape without touching the network.
func serveFixture(t *testing.T, status int, body []byte) (srv *httptest.Server, lastPath *string) {
	t.Helper()
	var path string
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &path
}

func loadADSBLolFixture(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/adsblol_point.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return body
}

// A box around the fixture's aircraft, wide enough to hold all of them.
const (
	boxLatMin, boxLatMax = -34.0, -33.85
	boxLonMin, boxLonMax = 150.9, 151.3
)

func statesByHex(states []State) map[string]State {
	out := make(map[string]State, len(states))
	for _, s := range states {
		out[s.ICAO24] = s
	}
	return out
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.01 }

// The failure this guards against would not be loud. Every aircraft would sit at
// three times its real height and move at half its real speed, and the provider
// would go on matching confidently - to the wrong one.
func TestADSBLolConvertsAviationUnits(t *testing.T) {
	t.Parallel()

	srv, _ := serveFixture(t, http.StatusOK, loadADSBLolFixture(t))
	c := &ADSBLolClient{HTTP: srv.Client(), BaseURL: srv.URL}

	states, err := c.StatesInBox(t.Context(), boxLatMin, boxLonMin, boxLatMax, boxLonMax)
	if err != nil {
		t.Fatalf("StatesInBox: %v", err)
	}
	s, ok := statesByHex(states)["7c7f53"]
	if !ok {
		t.Fatalf("VH-ZFP missing from %d states", len(states))
	}

	// 1600 ft geometric, 1450 ft barometric, 86.3 kt.
	if !near(s.GeoAltitudeM, 1600*0.3048) {
		t.Errorf("geometric altitude %.1f m, want %.1f m (1600 ft)", s.GeoAltitudeM, 1600*0.3048)
	}
	if !near(s.BaroAltitudeM, 1450*0.3048) {
		t.Errorf("barometric altitude %.1f m, want %.1f m (1450 ft)", s.BaroAltitudeM, 1450*0.3048)
	}
	if !near(s.VelocityMS, 86.3*0.514444) {
		t.Errorf("speed %.2f m/s, want %.2f m/s (86.3 kt)", s.VelocityMS, 86.3*0.514444)
	}
	if s.AltitudeSource != "geometric" {
		t.Errorf("altitude source %q, want geometric when alt_geom is present", s.AltitudeSource)
	}
	if s.Callsign != "ZFP" && s.Callsign == "" {
		t.Errorf("callsign %q not trimmed or not read", s.Callsign)
	}
}

// alt_baro is a number of feet, or the string "ground". Decoding it as a plain
// float makes every aircraft on the ground a parse error - and a decoder that
// skipped errors would quietly count them as airborne at sea level.
func TestADSBLolRecognisesTheGround(t *testing.T) {
	t.Parallel()

	srv, _ := serveFixture(t, http.StatusOK, loadADSBLolFixture(t))
	c := &ADSBLolClient{HTTP: srv.Client(), BaseURL: srv.URL}

	states, err := c.StatesInBox(t.Context(), boxLatMin, boxLonMin, boxLatMax, boxLonMax)
	if err != nil {
		t.Fatalf("StatesInBox: %v", err)
	}
	s, ok := statesByHex(states)["7c794f"]
	if !ok {
		t.Fatal("the aircraft on the ground is missing entirely")
	}
	if !s.OnGround {
		t.Error("alt_baro \"ground\" did not set OnGround")
	}
	if s.AltitudeSource != "none" {
		t.Errorf("altitude source %q for a grounded aircraft with no altitude, want none", s.AltitudeSource)
	}
}

func TestADSBLolFallsBackToBarometric(t *testing.T) {
	t.Parallel()

	srv, _ := serveFixture(t, http.StatusOK, loadADSBLolFixture(t))
	c := &ADSBLolClient{HTTP: srv.Client(), BaseURL: srv.URL}

	states, err := c.StatesInBox(t.Context(), boxLatMin, boxLonMin, boxLatMax, boxLonMax)
	if err != nil {
		t.Fatalf("StatesInBox: %v", err)
	}
	s := statesByHex(states)["7c6380"] // alt_baro 800, no alt_geom
	if s.AltitudeSource != "barometric" {
		t.Errorf("altitude source %q, want barometric", s.AltitudeSource)
	}
	if !near(s.AltitudeM(), 800*0.3048) {
		t.Errorf("altitude %.1f m, want %.1f m", s.AltitudeM(), 800*0.3048)
	}
}

// The endpoint answers about a circle; StatesInBox promises a box. Filtering the
// answer back down is what lets either source stand in for the other without
// the provider knowing which one it got.
func TestADSBLolFiltersTheCircleBackToTheBox(t *testing.T) {
	t.Parallel()

	srv, _ := serveFixture(t, http.StatusOK, loadADSBLolFixture(t))
	c := &ADSBLolClient{HTTP: srv.Client(), BaseURL: srv.URL}

	// A box that excludes VH-ZFP at -33.8998.
	states, err := c.StatesInBox(t.Context(), -34.0, 150.9, -33.91, 151.3)
	if err != nil {
		t.Fatalf("StatesInBox: %v", err)
	}
	if _, present := statesByHex(states)["7c7f53"]; present {
		t.Error("an aircraft outside the box was returned")
	}
}

func TestADSBLolAsksAboutTheBoxCentre(t *testing.T) {
	t.Parallel()

	srv, path := serveFixture(t, http.StatusOK, []byte(`{"ac":[]}`))
	c := &ADSBLolClient{HTTP: srv.Client(), BaseURL: srv.URL}

	if _, err := c.StatesInBox(t.Context(), -34.2, 150.7, -34.0, 150.9); err != nil {
		t.Fatalf("StatesInBox: %v", err)
	}
	if !strings.HasPrefix(*path, "/v2/point/-34.10000/150.80000/") {
		t.Errorf("requested %q, want the box centre -34.1, 150.8", *path)
	}
}

// Their limit is undocumented, so a 429 is the only way it shows itself. Mapped
// to the same sentinel OpenSky uses, so a chain need not know whose limit it
// was.
func TestADSBLolReportsItsRateLimitAsTheCreditSentinel(t *testing.T) {
	t.Parallel()

	srv, _ := serveFixture(t, http.StatusTooManyRequests, []byte(`{}`))
	c := &ADSBLolClient{HTTP: srv.Client(), BaseURL: srv.URL}

	_, err := c.StatesInBox(t.Context(), boxLatMin, boxLonMin, boxLatMax, boxLonMax)
	if !errors.Is(err, ErrCreditFloor) {
		t.Fatalf("got %v, want ErrCreditFloor", err)
	}
}

func TestCoveringRadiusContainsTheWholeBox(t *testing.T) {
	t.Parallel()

	// The station's 12 km search box, which must fit inside the circle.
	latMin, lonMin, latMax, lonMax := BoundingBox(radiusStation, 12000)
	radiusNM := coveringRadiusNM(latMin, lonMin, latMax, lonMax)

	radiusM := float64(radiusNM) * metresPerNauticalMile
	if radiusM < 12000*math.Sqrt2 {
		t.Errorf("radius %d NM (%.0f m) does not reach the corners of a 12 km box", radiusNM, radiusM)
	}
	if radiusNM > 12 {
		t.Errorf("radius %d NM is far larger than the box needs", radiusNM)
	}
}

// ---- FallbackSource ---------------------------------------------------------

type scriptedSource struct {
	name    string
	states  []State
	err     error
	credits int
	known   bool
	calls   int
}

func (s *scriptedSource) Name() string { return s.name }
func (s *scriptedSource) StatesInBox(context.Context, float64, float64, float64, float64) ([]State, error) {
	s.calls++
	return s.states, s.err
}
func (s *scriptedSource) CreditsRemaining() (int, bool) { return s.credits, s.known }

// The case this exists for: the primary has hit its credit floor, and the
// station should go on identifying aircraft instead of spending the morning
// blind.
func TestFallbackUsesTheSecondSourceWhenTheFirstIsSpent(t *testing.T) {
	t.Parallel()

	primary := &scriptedSource{name: "opensky", err: ErrCreditFloor}
	backup := &scriptedSource{name: "adsb.lol", states: []State{{ICAO24: "7c617e"}}}
	var passedOver []string
	f := &FallbackSource{
		Sources:    []StateSource{primary, backup},
		OnFallback: func(name string, _ error) { passedOver = append(passedOver, name) },
	}

	states, err := f.StatesInBox(t.Context(), 0, 0, 1, 1)
	if err != nil {
		t.Fatalf("StatesInBox: %v", err)
	}
	if len(states) != 1 || states[0].ICAO24 != "7c617e" {
		t.Fatalf("got %+v, want the backup's answer", states)
	}
	if len(passedOver) != 1 || passedOver[0] != "opensky" {
		t.Fatalf("passed over %v, want opensky reported so a failing primary is visible", passedOver)
	}
}

// The primary answering must not cost a request to the backup.
func TestFallbackDoesNotTouchTheBackupWhenThePrimaryAnswers(t *testing.T) {
	t.Parallel()

	primary := &scriptedSource{name: "opensky", states: []State{{ICAO24: "abc"}}}
	backup := &scriptedSource{name: "adsb.lol"}
	f := &FallbackSource{Sources: []StateSource{primary, backup}}

	if _, err := f.StatesInBox(t.Context(), 0, 0, 1, 1); err != nil {
		t.Fatalf("StatesInBox: %v", err)
	}
	if backup.calls != 0 {
		t.Fatalf("backup called %d times while the primary was answering", backup.calls)
	}
}

// A broken primary is a more common failure than an exhausted one. Falling
// through only on the credit sentinel would leave the station blind whenever
// OpenSky was merely down.
func TestFallbackAlsoCoversAPrimaryThatIsBrokenRatherThanSpent(t *testing.T) {
	t.Parallel()

	primary := &scriptedSource{name: "opensky", err: errors.New("connection reset")}
	backup := &scriptedSource{name: "adsb.lol", states: []State{{ICAO24: "abc"}}}
	f := &FallbackSource{Sources: []StateSource{primary, backup}}

	states, err := f.StatesInBox(t.Context(), 0, 0, 1, 1)
	if err != nil || len(states) != 1 {
		t.Fatalf("got %v, %v; want the backup's answer", states, err)
	}
}

func TestFallbackReportsEveryFailureWhenAllFail(t *testing.T) {
	t.Parallel()

	f := &FallbackSource{Sources: []StateSource{
		&scriptedSource{name: "opensky", err: ErrCreditFloor},
		&scriptedSource{name: "adsb.lol", err: errors.New("503")},
	}}

	_, err := f.StatesInBox(t.Context(), 0, 0, 1, 1)
	if err == nil {
		t.Fatal("got nil error with every source failing")
	}
	if !errors.Is(err, ErrCreditFloor) {
		t.Errorf("the joined error lost the primary's cause: %v", err)
	}
	if !strings.Contains(err.Error(), "adsb.lol") {
		t.Errorf("the joined error does not name the backup: %v", err)
	}
}

// A cancelled context is the caller giving up. Trying the backup would spend a
// second request on an answer nobody is waiting for.
func TestFallbackStopsWhenTheCallerGivesUp(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	primary := &scriptedSource{name: "opensky", err: context.Canceled}
	backup := &scriptedSource{name: "adsb.lol"}
	f := &FallbackSource{Sources: []StateSource{primary, backup}}

	_, _ = f.StatesInBox(ctx, 0, 0, 1, 1)
	if backup.calls != 0 {
		t.Fatal("backup was asked after the caller had already cancelled")
	}
}

// Callers use the balance to decide when to stop spending. With an unmetered
// source behind the metered one there is nothing to stop for, and reporting a
// ceiling would idle a collector that could have kept working.
func TestFallbackReportsNoCeilingWhenAnUnmeteredSourceIsBehind(t *testing.T) {
	t.Parallel()

	f := &FallbackSource{Sources: []StateSource{
		&scriptedSource{name: "opensky", credits: 0, known: true},
		NewADSBLolClient(),
	}}

	if _, known := f.CreditsRemaining(); known {
		t.Fatal("reported a credit ceiling although adsb.lol would take over")
	}
}

func TestFallbackReportsThePrimaryBalanceWhileItLasts(t *testing.T) {
	t.Parallel()

	f := &FallbackSource{Sources: []StateSource{
		&scriptedSource{name: "opensky", credits: 3200, known: true},
		NewADSBLolClient(),
	}}

	if c, known := f.CreditsRemaining(); !known || c != 3200 {
		t.Fatalf("got %d/%v, want the primary's 3200", c, known)
	}
}

// ---- helpers ----------------------------------------------------------------

// radiusStation is the deployment station. This file is the internal test
// package so it can reach the unexported radius helper, which means it cannot
// share the external package's station variable.
var radiusStation = enrichment.Station{Latitude: -34.11159, Longitude: 150.79226, ElevationM: 140}

// The omission that broke the fallback within minutes of it going live. The
// reuse window lives inside the OpenSky client, so the backup inherited none of
// it, took every enrichment query raw - several a second during a busy pass -
// and was rate limited immediately, entirely fairly.
func TestADSBLolReusesAFetchedSky(t *testing.T) {
	t.Parallel()

	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"ac":[]}`))
	}))
	t.Cleanup(srv.Close)

	c := &ADSBLolClient{HTTP: srv.Client(), BaseURL: srv.URL, StateTTL: 10 * time.Second}
	now := time.Now()
	c.SetClock(func() time.Time { return now })

	for range 5 {
		if _, err := c.StatesInBox(t.Context(), -34.2, 150.7, -34.0, 150.9); err != nil {
			t.Fatalf("StatesInBox: %v", err)
		}
	}
	if requests != 1 {
		t.Fatalf("made %d requests for five queries inside the window, want 1", requests)
	}

	now = now.Add(11 * time.Second)
	if _, err := c.StatesInBox(t.Context(), -34.2, 150.7, -34.0, 150.9); err != nil {
		t.Fatalf("StatesInBox after the window: %v", err)
	}
	if requests != 2 {
		t.Fatalf("made %d requests, want a fresh one once the window passed", requests)
	}
}

// A different box is a different question and must not be answered from the
// cache.
func TestADSBLolDoesNotReuseAcrossBoxes(t *testing.T) {
	t.Parallel()

	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"ac":[]}`))
	}))
	t.Cleanup(srv.Close)

	c := &ADSBLolClient{HTTP: srv.Client(), BaseURL: srv.URL, StateTTL: 10 * time.Second}
	now := time.Now()
	c.SetClock(func() time.Time { return now })

	_, _ = c.StatesInBox(t.Context(), -34.2, 150.7, -34.0, 150.9)
	_, _ = c.StatesInBox(t.Context(), -35.2, 151.7, -35.0, 151.9)
	if requests != 2 {
		t.Fatalf("made %d requests for two different boxes, want 2", requests)
	}
}

// Their documentation asks for responsible use. Go's default user agent says
// nothing about who is calling or why.
func TestADSBLolIdentifiesItself(t *testing.T) {
	t.Parallel()

	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(`{"ac":[]}`))
	}))
	t.Cleanup(srv.Close)

	c := &ADSBLolClient{HTTP: srv.Client(), BaseURL: srv.URL, StateTTL: -1}
	if _, err := c.StatesInBox(t.Context(), -34.2, 150.7, -34.0, 150.9); err != nil {
		t.Fatalf("StatesInBox: %v", err)
	}
	if !strings.Contains(seen, "SoundNet") {
		t.Fatalf("user agent %q does not name the station", seen)
	}
}

// Measured at the station: seven requests in ten succeed at a ten-second
// interval, with no Retry-After to wait for. Failing the other three outright
// loses an identification for no reason when a recent sky is in memory.
func TestADSBLolServesARecentSkyWhenRefused(t *testing.T) {
	t.Parallel()

	var status int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if status == http.StatusTooManyRequests {
			w.WriteHeader(status)
			return
		}
		_, _ = w.Write([]byte(`{"ac":[{"hex":"7c617e","flight":"RSCU208","lat":-34.10,"lon":150.79,` +
			`"alt_geom":800,"gs":140,"track":90}]}`))
	}))
	t.Cleanup(srv.Close)

	c := &ADSBLolClient{HTTP: srv.Client(), BaseURL: srv.URL, StateTTL: 5 * time.Second}
	now := time.Now()
	c.SetClock(func() time.Time { return now })

	fresh, err := c.StatesInBox(t.Context(), -34.3, 150.6, -33.9, 151.0)
	if err != nil || len(fresh) != 1 {
		t.Fatalf("first fetch: %v, %d states", err, len(fresh))
	}

	// Past the reuse window, and now refused.
	now = now.Add(20 * time.Second)
	status = http.StatusTooManyRequests

	stale, err := c.StatesInBox(t.Context(), -34.3, 150.6, -33.9, 151.0)
	if err != nil {
		t.Fatalf("got %v, want the recent sky rather than a failure", err)
	}
	if len(stale) != 1 {
		t.Fatalf("got %d states, want the cached one", len(stale))
	}

	// Dead reckoned, not replayed. Note the unit: gs is 140 *knots*, which is
	// 72 m/s, so 20 seconds due east is 1.44 km - about 0.0156 degrees of
	// longitude at this latitude. Writing this expectation in m/s first, and
	// watching it fail by exactly half, is the same trap the conversion
	// constants in the client exist to stop.
	moved := stale[0].Longitude - fresh[0].Longitude
	if moved < 0.014 || moved > 0.018 {
		t.Fatalf("longitude moved by %.4f degrees, want about 0.0156 for 20s at 140 kt east", moved)
	}
	if stale[0].Latitude < fresh[0].Latitude-0.001 || stale[0].Latitude > fresh[0].Latitude+0.001 {
		t.Errorf("latitude moved although the track is due east")
	}
}

// Past the stale limit the assumption of constant track and speed stops being
// fair, and a wrong position is worse than no answer.
func TestADSBLolRefusesAnAncientSky(t *testing.T) {
	t.Parallel()

	var status int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if status == http.StatusTooManyRequests {
			w.WriteHeader(status)
			return
		}
		_, _ = w.Write([]byte(`{"ac":[{"hex":"7c617e","lat":-34.10,"lon":150.79,"alt_geom":800,` +
			`"gs":140,"track":90}]}`))
	}))
	t.Cleanup(srv.Close)

	c := &ADSBLolClient{HTTP: srv.Client(), BaseURL: srv.URL, StateTTL: 5 * time.Second}
	now := time.Now()
	c.SetClock(func() time.Time { return now })
	if _, err := c.StatesInBox(t.Context(), -34.3, 150.6, -33.9, 151.0); err != nil {
		t.Fatalf("first fetch: %v", err)
	}

	now = now.Add(MaxStaleAge + time.Second)
	status = http.StatusTooManyRequests

	if _, err := c.StatesInBox(t.Context(), -34.3, 150.6, -33.9, 151.0); !errors.Is(err, ErrCreditFloor) {
		t.Fatalf("got %v, want a refusal rather than a position half a minute out", err)
	}
}
