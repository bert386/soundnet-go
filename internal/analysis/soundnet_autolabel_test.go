package analysis

import (
	"context"
	"testing"
	"time"

	"github.com/bert386/soundnet-go/internal/conf"
	"github.com/bert386/soundnet-go/internal/enrichment"
	"github.com/bert386/soundnet-go/internal/enrichment/adsb"
)

// The deployment station, so the geometry in these tests is the geometry that
// actually runs.
var testStation = enrichment.Station{Latitude: -34.9, Longitude: 138.6, ElevationM: 140}

type fakeStates struct {
	states []adsb.State
	err    error
	box    [4]float64
	calls  int
}

func (f *fakeStates) StatesInBox(_ context.Context, latMin, lonMin, latMax, lonMax float64) ([]adsb.State, error) {
	f.calls++
	f.box = [4]float64{latMin, lonMin, latMax, lonMax}
	return f.states, f.err
}

type fakeMetadata struct {
	info  map[string]*adsb.AircraftInfo
	calls []string
}

func (f *fakeMetadata) Aircraft(_ context.Context, hex string) (*adsb.AircraftInfo, error) {
	f.calls = append(f.calls, hex)
	if info, ok := f.info[hex]; ok {
		return info, nil
	}
	return nil, adsb.ErrMetadataNotFound
}

func (f *fakeMetadata) Route(_ context.Context, _ string) (*adsb.RouteInfo, error) {
	return nil, adsb.ErrMetadataNotFound
}

// airborne returns a state close to the station at the given altitude.
func airborne(hex string, altitudeM float64) adsb.State {
	return adsb.State{
		ICAO24:         hex,
		Callsign:       " QFA557 ",
		Latitude:       testStation.Latitude + 0.005,
		Longitude:      testStation.Longitude,
		GeoAltitudeM:   altitudeM,
		VelocityMS:     180,
		TrackDeg:       90,
		HasPosition:    true,
		AltitudeSource: "geometric",
	}
}

func TestCaptureWindowCentresOnTheInstant(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 20, 21, 0, 30, 0, time.UTC)
	at := now.Add(-10 * time.Second)

	start, end := captureWindow(at, 3*time.Second, now)

	if got := end.Sub(start); got != 3*time.Second {
		t.Fatalf("window length = %v, want 3s", got)
	}
	if mid := start.Add(end.Sub(start) / 2); !mid.Equal(at) {
		t.Fatalf("window centred on %v, want %v", mid, at)
	}
}

// A short acoustic lag puts the far edge of the window in the future, where
// ReadSegment refuses to go and will not wait. The window must slide back
// rather than shrink: a clip of a different length is a different kind of
// training example from every other one in the corpus.
func TestCaptureWindowSlidesBackInsteadOfShrinking(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 20, 21, 0, 30, 0, time.UTC)
	at := now.Add(-500 * time.Millisecond)

	start, end := captureWindow(at, 3*time.Second, now)

	if got := end.Sub(start); got != 3*time.Second {
		t.Fatalf("window length = %v, want the full 3s", got)
	}
	if !end.Before(now) {
		t.Fatalf("window ends at %v, which is not in the past relative to %v", end, now)
	}
	if got := now.Sub(end); got != captureGuard {
		t.Fatalf("window ends %v before now, want the %v guard", got, captureGuard)
	}
}

func TestOverheadSkipsWhatCannotHaveMadeAnAirborneSound(t *testing.T) {
	t.Parallel()

	onGround := airborne("aaa111", 500)
	onGround.OnGround = true
	noPosition := airborne("bbb222", 500)
	noPosition.HasPosition = false
	noAltitude := airborne("ccc333", 500)
	noAltitude.AltitudeSource = "none"

	states := &fakeStates{states: []adsb.State{onGround, noPosition, noAltitude, airborne("ddd444", 800)}}
	sky := &openSkySky{states: states, credits: func() (int, bool) { return 0, false }}

	got, err := sky.Overhead(t.Context(), testStation, 8000)
	if err != nil {
		t.Fatalf("Overhead: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d aircraft, want only the airborne one: %+v", len(got), got)
	}
	if got[0].ICAO24 != "ddd444" {
		t.Fatalf("kept %q, want ddd444", got[0].ICAO24)
	}
	if got[0].Callsign != "QFA557" {
		t.Fatalf("callsign = %q, want it trimmed to QFA557", got[0].Callsign)
	}
	if got[0].SlantM <= 0 {
		t.Fatalf("slant range = %v, want a positive distance", got[0].SlantM)
	}
	if got[0].Lag <= 0 {
		t.Fatalf("acoustic lag = %v, want a positive delay", got[0].Lag)
	}
}

// The search box is wider than the capture range on purpose, so the metadata
// lookup must not fire for traffic the collector would never record: it is a
// free community database and the sample is never written.
func TestOverheadLooksUpMetadataOnlyWithinCaptureRange(t *testing.T) {
	t.Parallel()

	near := airborne("near01", 800)
	far := airborne("far001", 800)
	far.Latitude = testStation.Latitude + 0.05 // several kilometres out

	states := &fakeStates{states: []adsb.State{near, far}}
	meta := &fakeMetadata{info: map[string]*adsb.AircraftInfo{
		"near01": {TypeCode: "B738", Registration: "VH-VOL"},
		"far001": {TypeCode: "A320"},
	}}
	sky := &openSkySky{
		states:          states,
		credits:         func() (int, bool) { return 0, false },
		metadata:        meta,
		metadataWithinM: 2000,
	}

	got, err := sky.Overhead(t.Context(), testStation, 12000)
	if err != nil {
		t.Fatalf("Overhead: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d aircraft, want both", len(got))
	}
	if len(meta.calls) != 1 || meta.calls[0] != "near01" {
		t.Fatalf("metadata lookups = %v, want only near01", meta.calls)
	}

	byHex := map[string]map[string]any{}
	for _, a := range got {
		byHex[a.ICAO24] = a.Attributes
	}
	if byHex["near01"]["type_code"] != "B738" {
		t.Fatalf("near aircraft type_code = %v, want B738", byHex["near01"]["type_code"])
	}
	if _, present := byHex["far001"]["type_code"]; present {
		t.Fatalf("far aircraft carries a type_code it was never looked up for: %v", byHex["far001"])
	}
}

// An unresolved type must leave the key absent rather than empty. The corpus
// files a missing type under _untyped, where the sample stays usable; an empty
// string would instead create a directory named for nothing and separate those
// samples from the untyped ones they belong with.
func TestOverheadLeavesUnresolvedMetadataAbsent(t *testing.T) {
	t.Parallel()

	states := &fakeStates{states: []adsb.State{airborne("unk001", 800)}}
	meta := &fakeMetadata{info: map[string]*adsb.AircraftInfo{}}
	sky := &openSkySky{
		states:          states,
		credits:         func() (int, bool) { return 0, false },
		metadata:        meta,
		metadataWithinM: 10000,
	}

	got, err := sky.Overhead(t.Context(), testStation, 12000)
	if err != nil {
		t.Fatalf("Overhead: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d aircraft, want 1", len(got))
	}
	if len(got[0].Attributes) != 0 {
		t.Fatalf("attributes = %v, want none written for an unresolved lookup", got[0].Attributes)
	}
}

func TestAutoLabelSourceID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		configured string
		available  []string
		want       string
		wantErr    bool
	}{
		{name: "one source needs no configuration", available: []string{"mic-a"}, want: "mic-a"},
		{name: "configured source is honoured", configured: "mic-b", available: []string{"mic-a", "mic-b"}, want: "mic-b"},
		{name: "several sources refuse to be guessed between", available: []string{"mic-a", "mic-b"}, wantErr: true},
		{name: "configured source that is not running is an error", configured: "mic-z", available: []string{"mic-a"}, wantErr: true},
		{name: "no sources at all", available: nil, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := autoLabelSourceID(tc.configured, tc.available)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("got %q, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("autoLabelSourceID: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// The two consumers share one daily allowance, and the collector polls whether
// or not anything flew while runtime enrichment only spends a credit on a sound
// that was actually heard. So the collector must stop first.
func TestAutoLabelReserveExceedsRuntimeCreditFloor(t *testing.T) {
	t.Parallel()

	d := conf.DefaultSoundNetSettings()
	if d.AutoLabel.CreditReserve <= d.Enrichment.ADSB.CreditFloor {
		t.Fatalf("collector reserve %d does not exceed the runtime floor %d; the collector would go on polling after runtime enrichment had stopped",
			d.AutoLabel.CreditReserve, d.Enrichment.ADSB.CreditFloor)
	}
}
