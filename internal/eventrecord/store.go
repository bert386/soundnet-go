package eventrecord

import (
	"encoding/json"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrNotFound is returned when a record does not exist. Callers distinguish this
// from a real failure with errors.Is: for diagnostics and enrichment, absence is
// an ordinary and expected state, not an error condition.
var ErrNotFound = errors.New("eventrecord: not found")

// ReviewState is the operator-facing review status of a detection. It is derived
// rather than stored: see the package comment for how each state is represented.
type ReviewState string

const (
	ReviewUnreviewed    ReviewState = "unreviewed"
	ReviewConfirmed     ReviewState = "confirmed"
	ReviewCorrected     ReviewState = "corrected"
	ReviewFalsePositive ReviewState = "false_positive"
)

// Store persists the SoundNet layers of a detection.
type Store struct {
	db *gorm.DB
}

// NewStore returns a Store over an existing database handle. It does not migrate;
// call Migrate explicitly so schema changes happen at a moment the caller chooses.
func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// Migrate creates or updates this package's tables. It is idempotent, and it only
// ever touches tables in the soundnet_ namespace, so it cannot disturb upstream's
// schema or its migration bookkeeping.
func (s *Store) Migrate() error {
	if err := s.db.AutoMigrate(AllEntities()...); err != nil {
		return fmt.Errorf("eventrecord: migrate: %w", err)
	}
	return nil
}

// PutDiagnostics stores or replaces the diagnostics document for a detection.
// Recomputing diagnostics for a clip - which the UI allows while tuning
// thresholds - must overwrite rather than accumulate, hence the upsert.
func (s *Store) PutDiagnostics(detectionID uint, doc any, schemaVersion int, computeMs int64) error {
	payload, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("eventrecord: marshal diagnostics: %w", err)
	}
	rec := Diagnostics{
		DetectionID:   detectionID,
		SchemaVersion: schemaVersion,
		Payload:       json.RawMessage(payload),
		ComputeMs:     computeMs,
	}
	err = s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "detection_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"schema_version", "payload", "compute_ms"}),
	}).Create(&rec).Error
	if err != nil {
		return fmt.Errorf("eventrecord: put diagnostics for detection %d: %w", detectionID, err)
	}
	return nil
}

// GetDiagnostics decodes the diagnostics document for a detection into out.
// Returns ErrNotFound when no diagnostics have been computed, which is normal
// for a detection whose class has diagnostics disabled.
func (s *Store) GetDiagnostics(detectionID uint, out any) (schemaVersion int, err error) {
	var rec Diagnostics
	if err := s.db.Where("detection_id = ?", detectionID).First(&rec).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("eventrecord: get diagnostics for detection %d: %w", detectionID, err)
	}
	if out != nil && len(rec.Payload) > 0 {
		if err := json.Unmarshal(rec.Payload, out); err != nil {
			return rec.SchemaVersion, fmt.Errorf("eventrecord: decode diagnostics for detection %d: %w", detectionID, err)
		}
	}
	return rec.SchemaVersion, nil
}

// PutEnrichment stores or replaces one provider's identity resolution.
//
// Callers must only call this when a provider actually resolved an identity.
// A provider that found nothing writes no row: absence is the honest record, and
// a row with an empty payload would later be indistinguishable from a real match.
func (s *Store) PutEnrichment(e *Enrichment) error {
	if e.Provider == "" {
		return errors.New("eventrecord: enrichment requires a provider")
	}
	err := s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "detection_id"}, {Name: "provider"}},
		DoUpdates: clause.AssignmentColumns([]string{"source", "payload", "confidence", "lag_correction_ms"}),
	}).Create(e).Error
	if err != nil {
		return fmt.Errorf("eventrecord: put %s enrichment for detection %d: %w", e.Provider, e.DetectionID, err)
	}
	return nil
}

// GetEnrichment returns one provider's identity resolution for a detection.
func (s *Store) GetEnrichment(detectionID uint, provider string) (*Enrichment, error) {
	var rec Enrichment
	err := s.db.Where("detection_id = ? AND provider = ?", detectionID, provider).First(&rec).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("eventrecord: get %s enrichment for detection %d: %w", provider, detectionID, err)
	}
	return &rec, nil
}

// PutCorrection records an operator reclassifying a detection. Re-correcting the
// same detection replaces the previous correction but preserves the original
// label, so the confusion pair stays anchored to what the model actually said.
func (s *Store) PutCorrection(detectionID, originalLabelID, correctedLabelID uint, note string) error {
	if correctedLabelID == 0 {
		return errors.New("eventrecord: correction requires a corrected label")
	}
	if correctedLabelID == originalLabelID {
		return errors.New("eventrecord: correction must differ from the original label")
	}
	rec := Correction{
		DetectionID:      detectionID,
		OriginalLabelID:  originalLabelID,
		CorrectedLabelID: correctedLabelID,
		Note:             note,
	}
	err := s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "detection_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"corrected_label_id", "note"}),
	}).Create(&rec).Error
	if err != nil {
		return fmt.Errorf("eventrecord: put correction for detection %d: %w", detectionID, err)
	}
	return nil
}

// GetCorrection returns the operator's correction for a detection.
func (s *Store) GetCorrection(detectionID uint) (*Correction, error) {
	var rec Correction
	if err := s.db.Where("detection_id = ?", detectionID).First(&rec).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("eventrecord: get correction for detection %d: %w", detectionID, err)
	}
	return &rec, nil
}

// ReviewStateOf derives a detection's review state.
//
// verified is upstream's DetectionReview.Verified value, or "" when no review row
// exists. A correction wins over a plain confirmation: an operator who supplied
// the right answer has told us strictly more than one who only said "wrong".
func (s *Store) ReviewStateOf(detectionID uint, verified string) (ReviewState, error) {
	_, err := s.GetCorrection(detectionID)
	switch {
	case err == nil:
		return ReviewCorrected, nil
	case !errors.Is(err, ErrNotFound):
		return "", err
	}

	switch verified {
	case "correct":
		return ReviewConfirmed, nil
	case "false_positive":
		return ReviewFalsePositive, nil
	default:
		return ReviewUnreviewed, nil
	}
}
