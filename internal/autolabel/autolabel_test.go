package autolabel_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bert386/soundnet-go/internal/autolabel"
	"github.com/bert386/soundnet-go/internal/enrichment"
)

var station = enrichment.Station{Latitude: -34.11159024409095, Longitude: 150.7922555571461, ElevationM: 140}

type fakeSky struct {
	aircraft []autolabel.Aircraft
	err      error
	credits  int
	known    bool
	calls    int
}

func (f *fakeSky) Overhead(context.Context, enrichment.Station, float64) ([]autolabel.Aircraft, error) {
	f.calls++
	return f.aircraft, f.err
}
func (f *fakeSky) CreditsRemaining() (credits int, known bool) { return f.credits, f.known }

type fakeCapture struct {
	err   error
	calls int
}

func (f *fakeCapture) CaptureAround(context.Context, time.Time, time.Duration) (pcm []byte, sampleRate, bitDepth, channels int, err error) {
	f.calls++
	if f.err != nil {
		return nil, 0, 0, 0, f.err
	}
	return make([]byte, 96000), 48000, 16, 1, nil
}

type fakeCorpus struct {
	written []*autolabel.Sample
}

func (f *fakeCorpus) Write(_ context.Context, s *autolabel.Sample) (string, error) {
	f.written = append(f.written, s)
	return "/tmp/fake.wav", nil
}

func cfg() autolabel.Config {
	c := autolabel.DefaultConfig()
	c.Enabled = true
	return c
}

func TestCapturesAnUnambiguousOverflight(t *testing.T) {
	t.Parallel()
	sky := &fakeSky{aircraft: []autolabel.Aircraft{{
		ICAO24: "7c7801", Callsign: "QFA557", AltitudeM: 1200, SlantM: 1500,
		Lag: 4 * time.Second, Attributes: map[string]any{"type_code": "B738", "registration": "VH-XZN"},
	}}}
	capturer := &fakeCapture{}
	corpus := &fakeCorpus{}
	c := autolabel.New(cfg(), sky, capturer, corpus)

	d, err := c.Poll(t.Context(), station)
	require.NoError(t, err)
	require.True(t, d.Captured, "a single close, low aircraft is exactly what this is for; skipped: %s", d.Skipped)
	require.Len(t, corpus.written, 1)
	assert.Equal(t, "7c7801", corpus.written[0].Aircraft.ICAO24)
}

func TestRefusesWhenTwoAircraftAreAtSimilarRange(t *testing.T) {
	t.Parallel()
	sky := &fakeSky{aircraft: []autolabel.Aircraft{
		{ICAO24: "aaa111", AltitudeM: 1200, SlantM: 1500},
		{ICAO24: "bbb222", AltitudeM: 1300, SlantM: 2000},
	}}
	corpus := &fakeCorpus{}
	c := autolabel.New(cfg(), sky, &fakeCapture{}, corpus)

	d, err := c.Poll(t.Context(), station)
	require.NoError(t, err)

	// A wrong training label is worse than no sample: it teaches the model
	// something false, and that error propagates into every later prediction.
	assert.False(t, d.Captured)
	assert.Contains(t, d.Skipped, "guess")
	assert.Empty(t, corpus.written)
}

func TestIgnoresHighCruiseTraffic(t *testing.T) {
	t.Parallel()
	sky := &fakeSky{aircraft: []autolabel.Aircraft{
		{ICAO24: "high01", AltitudeM: 10000, SlantM: 3000},
	}}
	c := autolabel.New(cfg(), sky, &fakeCapture{}, &fakeCorpus{})

	d, err := c.Poll(t.Context(), station)
	require.NoError(t, err)
	// Geometrically close but usually inaudible under background noise, so the
	// clip would be labelled for an aircraft it does not actually contain.
	assert.False(t, d.Captured)
}

func TestIgnoresDistantAircraft(t *testing.T) {
	t.Parallel()
	sky := &fakeSky{aircraft: []autolabel.Aircraft{
		{ICAO24: "far001", AltitudeM: 1500, SlantM: 9000},
	}}
	c := autolabel.New(cfg(), sky, &fakeCapture{}, &fakeCorpus{})

	d, err := c.Poll(t.Context(), station)
	require.NoError(t, err)
	assert.False(t, d.Captured)
	assert.Contains(t, d.Skipped, "close")
}

func TestCooldownPreventsDuplicatesOfOneOverflight(t *testing.T) {
	t.Parallel()
	sky := &fakeSky{aircraft: []autolabel.Aircraft{
		{ICAO24: "7c7801", AltitudeM: 1200, SlantM: 1500},
	}}
	corpus := &fakeCorpus{}
	c := autolabel.New(cfg(), sky, &fakeCapture{}, corpus)

	first, err := c.Poll(t.Context(), station)
	require.NoError(t, err)
	require.True(t, first.Captured)

	// A single overflight spans several polls. Capturing each would fill the
	// corpus with near-duplicates and let a daily scheduled service dominate it.
	second, err := c.Poll(t.Context(), station)
	require.NoError(t, err)
	assert.False(t, second.Captured)
	assert.Contains(t, second.Skipped, "recently")
	assert.Len(t, corpus.written, 1)
}

func TestStopsAtCreditReserve(t *testing.T) {
	t.Parallel()
	sky := &fakeSky{
		aircraft: []autolabel.Aircraft{{ICAO24: "7c7801", AltitudeM: 1200, SlantM: 1500}},
		credits:  400, known: true,
	}
	c := autolabel.New(cfg(), sky, &fakeCapture{}, &fakeCorpus{})

	d, err := c.Poll(t.Context(), station)
	require.NoError(t, err)
	assert.False(t, d.Captured)
	assert.Contains(t, d.Skipped, "credit")
	// The sky must not even be read: runtime enrichment only spends a credit
	// when something was actually heard, so it is the better use of what remains.
	assert.Equal(t, 0, sky.calls, "no API call should be made below the reserve")
}

func TestMissingAudioIsNotAFailure(t *testing.T) {
	t.Parallel()
	sky := &fakeSky{aircraft: []autolabel.Aircraft{
		{ICAO24: "7c7801", AltitudeM: 1200, SlantM: 1500},
	}}
	corpus := &fakeCorpus{}
	c := autolabel.New(cfg(), sky, &fakeCapture{err: errors.New("buffer expired")}, corpus)

	d, err := c.Poll(t.Context(), station)
	// The aircraft was there; the audio simply was not retained long enough.
	// That is an ordinary miss, not an error worth stopping collection for.
	require.NoError(t, err)
	assert.False(t, d.Captured)
	assert.Contains(t, d.Skipped, "audio unavailable")
	assert.Empty(t, corpus.written)
}

func TestRunRequiresAStation(t *testing.T) {
	t.Parallel()
	c := autolabel.New(cfg(), &fakeSky{}, &fakeCapture{}, &fakeCorpus{})
	err := c.Run(t.Context(), enrichment.Station{})
	require.ErrorIs(t, err, enrichment.ErrNotConfigured)
}

func TestDisabledCollectorDoesNothing(t *testing.T) {
	t.Parallel()
	sky := &fakeSky{}
	c := autolabel.New(autolabel.DefaultConfig(), sky, &fakeCapture{}, &fakeCorpus{})
	require.NoError(t, c.Run(t.Context(), station))
	assert.Equal(t, 0, sky.calls, "a disabled collector must not poll an external API")
}

// --- corpus layout ---

func TestCorpusWritesWavAndLabels(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	fc := &autolabel.FileCorpus{Root: root}

	path, err := fc.Write(t.Context(), &autolabel.Sample{
		CapturedAt:  time.Date(2026, 9, 19, 22, 51, 12, 0, time.UTC),
		PCM:         make([]byte, 1000),
		SampleRate:  48000,
		BitDepth:    16,
		NumChannels: 1,
		Station:     station,
		Aircraft: autolabel.Aircraft{
			ICAO24: "7c7801", Callsign: "QFA557", AltitudeM: 1200, SlantM: 1500,
			Lag: 4400 * time.Millisecond,
			Attributes: map[string]any{
				"type_code": "B738", "registration": "VH-XZN", "operator": "Qantas",
			},
		},
	})
	require.NoError(t, err)

	// Grouped by type, which is the axis the aircraft-type head trains on.
	assert.Contains(t, path, filepath.Join("B738", "20260919T225112_7c7801_QFA557.wav"))

	// A real RIFF header, so any audio tool can open it. Raw PCM would lose the
	// sample rate and make the corpus useless once anyone forgot it.
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "RIFF", string(raw[0:4]))
	assert.Equal(t, "WAVE", string(raw[8:12]))
	assert.Len(t, raw, 44+1000)

	blob, err := os.ReadFile(path[:len(path)-4] + ".json")
	require.NoError(t, err)
	var meta map[string]any
	require.NoError(t, json.Unmarshal(blob, &meta))
	assert.Equal(t, "VH-XZN", meta["registration"])
	assert.Equal(t, "adsb-broadcast", meta["label_provenance"],
		"the label's origin must travel with it")
	assert.EqualValues(t, 4400, meta["acoustic_lag_ms"],
		"the lag is what ties this audio to that aircraft; without it the label cannot be re-checked")
}

func TestUntypedAircraftAreKeptNotDiscarded(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	fc := &autolabel.FileCorpus{Root: root}

	// Military, private and newly registered aircraft often have no type in any
	// public database. The audio and transponder identity are still correct, so
	// the example stays usable and the type can be filled in later.
	path, err := fc.Write(t.Context(), &autolabel.Sample{
		CapturedAt: time.Now(), PCM: make([]byte, 100), SampleRate: 48000, BitDepth: 16, NumChannels: 1,
		Aircraft: autolabel.Aircraft{ICAO24: "7cffff"},
	})
	require.NoError(t, err)
	assert.Contains(t, path, "_untyped")
}

func TestAwkwardTypeStringsBecomeSafePaths(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	fc := &autolabel.FileCorpus{Root: root}

	// Real type strings contain slashes and spaces - "737NG 838/W" - which would
	// otherwise create stray directories.
	path, err := fc.Write(t.Context(), &autolabel.Sample{
		CapturedAt: time.Now(), PCM: make([]byte, 100), SampleRate: 48000, BitDepth: 16, NumChannels: 1,
		Aircraft: autolabel.Aircraft{ICAO24: "abc123", Attributes: map[string]any{"type_code": "737NG 838/W"}},
	})
	require.NoError(t, err)
	assert.NotContains(t, filepath.Dir(path), "/737NG 838/")
	_, err = os.Stat(path)
	require.NoError(t, err)
}

// Without this the collector is invisible: it writes a file when it captures
// and does nothing at all otherwise, so "running and finding nothing" and
// "never started" look identical from outside. That is the failure this project
// has already had once, in the layer this one feeds.
func TestRunReportsEveryDecision(t *testing.T) {
	t.Parallel()

	cfg := autolabel.DefaultConfig()
	cfg.Enabled = true
	cfg.PollInterval = time.Millisecond

	c := autolabel.New(cfg, &fakeSky{}, nil, nil)
	seen := make(chan *autolabel.Decision, 4)
	c.OnDecision = func(d *autolabel.Decision) {
		select {
		case seen <- d:
		default:
		}
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = c.Run(ctx, station) }()

	select {
	case d := <-seen:
		assert.False(t, d.Captured, "an empty sky reported a capture")
		assert.NotEmpty(t, d.Skipped, "a refusal with no reason is the same as silence")
	case <-time.After(5 * time.Second):
		t.Fatal("the collector polled without reporting anything")
	}
}
