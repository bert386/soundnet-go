package eventpipeline_test

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bert386/soundnet-go/internal/enrichment"
	"github.com/bert386/soundnet-go/internal/eventpipeline"
	"github.com/bert386/soundnet-go/internal/eventrecord"
)

const sr = 16000

var station = enrichment.Station{Latitude: -34.11159024409095, Longitude: 150.7922555571461, ElevationM: 140}

// recordingStore captures what would have been persisted.
type recordingStore struct {
	diagnostics map[uint]any
	enrichments []*eventrecord.Enrichment
	failDiag    error
}

func newStore() *recordingStore {
	return &recordingStore{diagnostics: map[uint]any{}}
}

func (s *recordingStore) PutDiagnostics(id uint, doc any, _ int, _ int64) error {
	if s.failDiag != nil {
		return s.failDiag
	}
	s.diagnostics[id] = doc
	return nil
}

func (s *recordingStore) PutEnrichment(e *eventrecord.Enrichment) error {
	s.enrichments = append(s.enrichments, e)
	return nil
}

// stubResolver stands in for the enrichment registry.
type stubResolver struct {
	id      *enrichment.Identity
	err     error
	calls   int
	domains map[string]bool
}

func (r *stubResolver) Resolve(context.Context, *enrichment.Request) (*enrichment.Identity, error) {
	r.calls++
	return r.id, r.err
}

func (r *stubResolver) HasProviderFor(d string) bool { return r.domains[d] }

// tonePCM builds 16-bit little-endian mono PCM of a decaying tone.
func tonePCM(durationSec, freq float64) []byte {
	n := int(durationSec * sr)
	buf := make([]byte, n*2)
	for i := range n {
		t := float64(i) / sr
		v := math.Exp(-t*3) * math.Sin(2*math.Pi*freq*t) * 0.6
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(int16(v*math.MaxInt16)))
	}
	return buf
}

func baseInput(label string) *eventpipeline.Input {
	return &eventpipeline.Input{
		DetectionID: 42,
		Label:       label,
		Confidence:  0.8,
		DetectedAt:  time.Now(),
		PCM:         tonePCM(3, 700),
		SampleRate:  sr,
		BitDepth:    16,
		NumChannels: 1,
	}
}

func TestDecodePCMNormalises(t *testing.T) {
	t.Parallel()
	samples, err := eventpipeline.DecodePCM(tonePCM(0.1, 440), 16, 1)
	require.NoError(t, err)
	assert.Len(t, samples, int(0.1*sr))
	for _, v := range samples {
		assert.LessOrEqual(t, math.Abs(v), 1.0, "samples must be normalised to +/-1")
	}
}

func TestDecodePCMAveragesChannels(t *testing.T) {
	t.Parallel()
	// Two channels, both carrying the event: averaging keeps both rather than
	// discarding half the captured data.
	stereo := make([]byte, 8)
	putSample := func(b []byte, v int16) { binary.LittleEndian.PutUint16(b, uint16(v)) }
	putSample(stereo[0:], 1000)
	putSample(stereo[2:], 3000)
	putSample(stereo[4:], -2000)
	putSample(stereo[6:], -4000)

	samples, err := eventpipeline.DecodePCM(stereo, 16, 2)
	require.NoError(t, err)
	require.Len(t, samples, 2)
	assert.InDelta(t, 2000.0/math.MaxInt16, samples[0], 1e-6)
	assert.InDelta(t, -3000.0/math.MaxInt16, samples[1], 1e-6)
}

func TestDecodePCMRefusesUnknownFormat(t *testing.T) {
	t.Parallel()
	// Guessing at an unknown bit depth would turn noise into plausible-looking
	// measurements, which is worse than refusing.
	_, err := eventpipeline.DecodePCM([]byte{1, 2, 3, 4}, 24, 1)
	require.ErrorContains(t, err, "bit depth")
}

func TestBothLayersOffByDefault(t *testing.T) {
	t.Parallel()
	store := newStore()
	res := &stubResolver{domains: map[string]bool{"aircraft": true}}
	a := &eventpipeline.Analyser{Config: eventpipeline.DefaultConfig(), Store: store, Resolver: res}

	got, err := a.Process(t.Context(), baseInput("Aircraft"))
	require.NoError(t, err)

	// The scope requires new behaviour to default off where it adds cost or
	// makes outbound calls.
	assert.False(t, got.DiagnosticsRun)
	assert.False(t, got.EnrichmentRun)
	assert.Empty(t, store.diagnostics)
	assert.Equal(t, 0, res.calls, "nothing should reach the network by default")
}

func TestDiagnosticsRunForImpulse(t *testing.T) {
	t.Parallel()
	store := newStore()
	cfg := eventpipeline.DefaultConfig()
	cfg.DiagnosticsEnabled = true
	a := &eventpipeline.Analyser{Config: cfg, Store: store}

	got, err := a.Process(t.Context(), baseInput("Gunshot, gunfire"))
	require.NoError(t, err)
	assert.True(t, got.DiagnosticsRun)
	assert.Contains(t, store.diagnostics, uint(42))
	assert.Positive(t, got.DiagnosticsMs)
}

func TestDiagnosticsSkippedForDomainWithNothingToMeasure(t *testing.T) {
	t.Parallel()
	store := newStore()
	cfg := eventpipeline.DefaultConfig()
	cfg.DiagnosticsEnabled = true
	a := &eventpipeline.Analyser{Config: cfg, Store: store}

	// Speech is not a diagnosable domain. Running a Doppler fit on it would burn
	// Raspberry Pi cycles to produce nothing.
	got, err := a.Process(t.Context(), baseInput("Speech"))
	require.NoError(t, err)
	assert.False(t, got.DiagnosticsRun)
	assert.Empty(t, store.diagnostics)
	assert.NotEmpty(t, got.Skipped)
}

func TestEnrichmentOnlyForDomainsWithAnAuthority(t *testing.T) {
	t.Parallel()
	cfg := eventpipeline.DefaultConfig()
	cfg.EnrichmentEnabled = true
	cfg.Station = station

	for _, tc := range []struct {
		label     string
		wantCalls int
		why       string
	}{
		{"Aircraft", 1, "ADS-B is authoritative for aircraft"},
		{"Thunder", 1, "lightning networks corroborate thunder"},
		{"Gunshot, gunfire", 0, "no public authority identifies a gunshot"},
		{"Siren", 0, "no public authority identifies a siren"},
		{"Car passing by", 0, "no public authority identifies a passing car"},
	} {
		res := &stubResolver{err: enrichment.ErrNoMatch}
		a := &eventpipeline.Analyser{Config: cfg, Store: newStore(), Resolver: res}
		_, err := a.Process(t.Context(), baseInput(tc.label))
		require.NoError(t, err, tc.label)
		assert.Equal(t, tc.wantCalls, res.calls, "%s: %s", tc.label, tc.why)
	}
}

func TestNoEnrichmentRowWhenNothingResolved(t *testing.T) {
	t.Parallel()
	store := newStore()
	cfg := eventpipeline.DefaultConfig()
	cfg.EnrichmentEnabled = true
	cfg.Station = station
	a := &eventpipeline.Analyser{
		Config: cfg, Store: store,
		Resolver: &stubResolver{err: enrichment.ErrNoMatch},
	}

	got, err := a.Process(t.Context(), baseInput("Aircraft"))
	require.NoError(t, err, "finding nothing is an ordinary outcome, not an error")
	assert.True(t, got.EnrichmentRun)
	assert.False(t, got.IdentityResolved)
	assert.Empty(t, store.enrichments, "an empty row would be indistinguishable from a real match")
}

func TestIdentityIsStoredWithProvenance(t *testing.T) {
	t.Parallel()
	store := newStore()
	cfg := eventpipeline.DefaultConfig()
	cfg.EnrichmentEnabled = true
	cfg.Station = station
	a := &eventpipeline.Analyser{
		Config: cfg, Store: store,
		Resolver: &stubResolver{id: &enrichment.Identity{
			Provider: "adsb", Source: "opensky", Confidence: 0.91, LagCorrectionMs: 8800,
			Attributes: map[string]any{"hex": "7c7801", "registration": "VH-XZN", "flight_iata": "QF557"},
		}},
	}

	got, err := a.Process(t.Context(), baseInput("Aircraft"))
	require.NoError(t, err)
	assert.True(t, got.IdentityResolved)
	require.Len(t, store.enrichments, 1)

	rec := store.enrichments[0]
	assert.Equal(t, "adsb", rec.Provider)
	assert.Equal(t, "opensky", rec.Source)
	assert.EqualValues(t, 8800, rec.LagCorrectionMs, "the lag must be retained for later diagnosis")
	assert.Contains(t, string(rec.Payload), "VH-XZN")
}

func TestEnrichmentReportsMissingStationAsConfiguration(t *testing.T) {
	t.Parallel()
	cfg := eventpipeline.DefaultConfig()
	cfg.EnrichmentEnabled = true
	// Station deliberately unset.
	a := &eventpipeline.Analyser{Config: cfg, Store: newStore(), Resolver: &stubResolver{}}

	_, err := a.Process(t.Context(), baseInput("Aircraft"))
	// Must not look like a site where nothing is identifiable: that would hide a
	// missing setting indefinitely.
	require.ErrorIs(t, err, enrichment.ErrNotConfigured)
}

func TestLayersAreIndependent(t *testing.T) {
	t.Parallel()
	store := newStore()
	store.failDiag = errors.New("disk full")
	cfg := eventpipeline.DefaultConfig()
	cfg.DiagnosticsEnabled = true
	cfg.EnrichmentEnabled = true
	cfg.Station = station
	a := &eventpipeline.Analyser{
		Config: cfg, Store: store,
		Resolver: &stubResolver{id: &enrichment.Identity{Provider: "adsb", Source: "opensky"}},
	}

	got, err := a.Process(t.Context(), baseInput("Aircraft"))

	// Diagnostics failed, but identity still resolved and stored. Losing both
	// because one failed would be needless: they are independent answers.
	require.Error(t, err)
	assert.True(t, got.IdentityResolved)
	assert.Len(t, store.enrichments, 1)
}

func TestUnpersistedDetectionIsSkipped(t *testing.T) {
	t.Parallel()
	store := newStore()
	cfg := eventpipeline.DefaultConfig()
	cfg.DiagnosticsEnabled = true
	a := &eventpipeline.Analyser{Config: cfg, Store: store}

	in := baseInput("Gunshot, gunfire")
	in.DetectionID = 0

	got, err := a.Process(t.Context(), in)
	require.NoError(t, err)
	// Nothing to attach results to, so the work would be discarded immediately.
	assert.False(t, got.DiagnosticsRun)
	assert.Contains(t, got.Skipped, "not persisted")
}
