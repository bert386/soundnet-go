package eventrecord_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/bert386/soundnet-go/internal/eventrecord"
)

type diagDoc struct {
	SPLDb      float64 `json:"spl_db"`
	OnsetCount int     `json:"onset_count"`
	SpeedKmh   float64 `json:"speed_kmh,omitempty"`
}

// newStore gives each test its own isolated in-memory database.
//
// The DSN is keyed on the test name deliberately: a plain
// "file::memory:?cache=shared" is a single database shared by every connection in
// the process, so parallel tests trample each other's rows. MaxOpenConns(1) keeps
// SQLite's in-memory database alive for the whole test - it is destroyed when its
// last connection closes, which a pool can do between statements.
func newStore(t *testing.T) *eventrecord.Store {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	s := eventrecord.NewStore(db)
	require.NoError(t, s.Migrate())
	return s
}

func TestMigrateIsIdempotent(t *testing.T) {
	t.Parallel()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	s := eventrecord.NewStore(db)
	require.NoError(t, s.Migrate())
	require.NoError(t, s.Migrate(), "running migration twice must not fail")
}

func TestDiagnosticsRoundTrip(t *testing.T) {
	t.Parallel()
	s := newStore(t)

	in := diagDoc{SPLDb: 61.4, OnsetCount: 3, SpeedKmh: 72.5}
	require.NoError(t, s.PutDiagnostics(101, in, 1, 42))

	var out diagDoc
	version, err := s.GetDiagnostics(101, &out)
	require.NoError(t, err)
	assert.Equal(t, 1, version)
	assert.Equal(t, in, out)
}

func TestDiagnosticsAbsentIsNotAnError(t *testing.T) {
	t.Parallel()
	s := newStore(t)

	var out diagDoc
	_, err := s.GetDiagnostics(999, &out)
	// Absence is an ordinary state - a class with diagnostics disabled never
	// produces a document - so callers must be able to detect it precisely.
	require.ErrorIs(t, err, eventrecord.ErrNotFound)
}

func TestDiagnosticsRecomputeOverwrites(t *testing.T) {
	t.Parallel()
	s := newStore(t)

	require.NoError(t, s.PutDiagnostics(7, diagDoc{OnsetCount: 2}, 1, 10))
	// Re-tuning thresholds in the UI recomputes the same clip; the result must
	// replace the old document rather than accumulate a second row.
	require.NoError(t, s.PutDiagnostics(7, diagDoc{OnsetCount: 5}, 1, 11))

	var out diagDoc
	_, err := s.GetDiagnostics(7, &out)
	require.NoError(t, err)
	assert.Equal(t, 5, out.OnsetCount)
}

func TestEnrichmentPerProvider(t *testing.T) {
	t.Parallel()
	s := newStore(t)

	require.NoError(t, s.PutEnrichment(&eventrecord.Enrichment{
		DetectionID: 55, Provider: "adsb", Source: "opensky",
		Confidence: 0.93, LagCorrectionMs: 8800,
	}))
	require.NoError(t, s.PutEnrichment(&eventrecord.Enrichment{
		DetectionID: 55, Provider: "lightning", Source: "blitzortung", Confidence: 0.4,
	}))

	adsb, err := s.GetEnrichment(55, "adsb")
	require.NoError(t, err)
	assert.Equal(t, "opensky", adsb.Source)
	assert.EqualValues(t, 8800, adsb.LagCorrectionMs, "acoustic lag must be retained for later diagnosis")

	lightning, err := s.GetEnrichment(55, "lightning")
	require.NoError(t, err)
	assert.Equal(t, "blitzortung", lightning.Source)
}

func TestEnrichmentRequiresProvider(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	require.Error(t, s.PutEnrichment(&eventrecord.Enrichment{DetectionID: 1, Source: "opensky"}))
}

func TestNoEnrichmentRowWhenNothingResolved(t *testing.T) {
	t.Parallel()
	s := newStore(t)

	// A gunshot has no authoritative external source. Nothing is written, and the
	// absence must read back as "not found" rather than as an empty match.
	_, err := s.GetEnrichment(88, "adsb")
	require.ErrorIs(t, err, eventrecord.ErrNotFound)
}

func TestCorrectionKeepsConfusionPair(t *testing.T) {
	t.Parallel()
	s := newStore(t)

	require.NoError(t, s.PutCorrection(12, 400, 401, "backfire, not a gunshot"))
	got, err := s.GetCorrection(12)
	require.NoError(t, err)
	assert.EqualValues(t, 400, got.OriginalLabelID)
	assert.EqualValues(t, 401, got.CorrectedLabelID)

	// Re-correcting replaces the answer but must keep the original label, or the
	// confusion pair that makes this useful for retraining is lost.
	require.NoError(t, s.PutCorrection(12, 400, 402, ""))
	got, err = s.GetCorrection(12)
	require.NoError(t, err)
	assert.EqualValues(t, 400, got.OriginalLabelID)
	assert.EqualValues(t, 402, got.CorrectedLabelID)
}

func TestCorrectionRejectsNoOp(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	require.Error(t, s.PutCorrection(3, 10, 10, ""), "correcting to the same label is meaningless")
	require.Error(t, s.PutCorrection(3, 10, 0, ""), "a correction needs a target label")
}

func TestReviewStateDerivation(t *testing.T) {
	t.Parallel()
	s := newStore(t)

	for _, tc := range []struct {
		name     string
		verified string
		want     eventrecord.ReviewState
	}{
		{"no review row", "", eventrecord.ReviewUnreviewed},
		{"confirmed", "correct", eventrecord.ReviewConfirmed},
		{"false positive", "false_positive", eventrecord.ReviewFalsePositive},
	} {
		got, err := s.ReviewStateOf(1000, tc.verified)
		require.NoError(t, err, tc.name)
		assert.Equal(t, tc.want, got, tc.name)
	}

	// A correction outranks a bare confirmation: the operator supplied the right
	// answer, which is strictly more information than "correct".
	require.NoError(t, s.PutCorrection(1000, 1, 2, ""))
	got, err := s.ReviewStateOf(1000, "correct")
	require.NoError(t, err)
	assert.Equal(t, eventrecord.ReviewCorrected, got)
}
