// Package eventrecord persists the SoundNet-specific layers of a detection:
// computed properties (diagnostics), externally resolved identity (enrichment),
// and operator corrections.
//
// These live in side tables keyed by detection ID rather than as columns on the
// upstream Detection entity. That is deliberate: this fork tracks upstream, and
// every column added to an upstream entity is a merge conflict in a file that
// changes often. A one-to-one side table carries the same information and merges
// cleanly. It also keeps the three layers of the scope separable at the storage
// level - class stays upstream's concern, properties and identity are ours.
//
// Review state is expressed without touching upstream's VerificationStatus:
//
//	unreviewed      no DetectionReview row
//	confirmed       DetectionReview.Verified == "correct"
//	false_positive  DetectionReview.Verified == "false_positive"
//	corrected       a DetectionCorrection row exists, naming the correct label
package eventrecord

// JSON payloads are held as json.RawMessage rather than gorm.io/datatypes.JSON:
// the upstream module does not depend on gorm.io/datatypes, and a fork that has
// to stay mergeable should not add a module for a type alias it can express with
// the standard library.

import (
	"encoding/json"
	"time"
)

// Diagnostics holds DSP-computed properties for one detection: measurements, not
// labels. Payload is the JSON-encoded diagnostics document produced by the
// diagnostics engine; it is stored opaquely so the engine can evolve its schema
// without a database migration. SchemaVersion records which shape Payload is in,
// so a reader can reject or upgrade a document it does not understand.
type Diagnostics struct {
	ID          uint `gorm:"primaryKey"`
	DetectionID uint `gorm:"not null;uniqueIndex"`

	// SchemaVersion is the diagnostics document version Payload conforms to.
	SchemaVersion int `gorm:"not null;default:1"`

	// Payload is the diagnostics document (level, spectral tilt, onset count,
	// envelope duration, Doppler fit, impulse features - whichever stages ran).
	Payload json.RawMessage `gorm:"type:json"`

	// ComputeMs records how long the diagnostics engine took for this clip.
	// Kept as a column rather than inside Payload so the Raspberry Pi performance
	// budget can be queried directly instead of parsed out of JSON.
	ComputeMs int64 `gorm:"not null;default:0"`

	CreatedAt time.Time `gorm:"autoCreateTime;index"`
}

// TableName keeps SoundNet tables in their own namespace, so an upstream table
// added later cannot collide with one of ours.
func (Diagnostics) TableName() string { return "soundnet_diagnostics" }

// Enrichment holds an externally resolved identity for one detection - the
// answer to "which exact one was it", which no amount of audio analysis can
// provide. Rows exist only where an authoritative source returned a match.
//
// There is deliberately no row for a provider that found nothing. For sirens,
// gunshots and vehicles no public authority exists, and an absent row is the
// honest representation; inventing an identity would be a defect.
type Enrichment struct {
	ID          uint `gorm:"primaryKey"`
	DetectionID uint `gorm:"not null;index:idx_enrichment_detection_provider,unique"`

	// Provider names the resolver ("adsb", "lightning"). Part of the unique index
	// with DetectionID: one row per provider per detection, since a future
	// detection could plausibly be enriched by more than one source.
	Provider string `gorm:"size:32;not null;index:idx_enrichment_detection_provider,unique"`

	// Source names the concrete upstream the answer came from ("opensky",
	// "dump1090", "blitzortung"). Recorded because latency and trustworthiness
	// differ sharply between sources for the same provider.
	Source string `gorm:"size:32;not null"`

	// Payload is the provider-specific identity document. For ADS-B:
	// callsign, hex, type, registration, altitude, cpa_km and which altitude
	// field was used.
	Payload json.RawMessage `gorm:"type:json"`

	// Confidence is the resolver's own confidence in the match, 0..1. An ADS-B
	// match with one aircraft in the window is near 1; a crowded sky is lower.
	Confidence float64 `gorm:"not null;default:0"`

	// LagCorrectionMs is the acoustic propagation delay applied when selecting
	// the track: the detection was back-dated by this much before matching.
	// Stored so a bad match can be diagnosed later without recomputing geometry.
	LagCorrectionMs int64 `gorm:"not null;default:0"`

	CreatedAt time.Time `gorm:"autoCreateTime;index"`
}

func (Enrichment) TableName() string { return "soundnet_enrichment" }

// Correction records an operator reclassifying a detection: the model said one
// thing, a human says another. Its presence is what "corrected" review state
// means, so no change to upstream's VerificationStatus enum is required.
//
// Corrections are the raw material for retraining, which is why the original
// label is kept alongside the corrected one: a confusion pair is more useful
// than a bare answer.
type Correction struct {
	ID          uint `gorm:"primaryKey"`
	DetectionID uint `gorm:"not null;uniqueIndex"`

	// OriginalLabelID is the label the model emitted, captured at correction
	// time so the confusion pair survives even if the detection is re-pointed.
	OriginalLabelID uint `gorm:"not null;index"`

	// CorrectedLabelID is the label the operator says is correct. It references
	// the upstream labels table by ID; no foreign key constraint is declared, to
	// avoid coupling our migration ordering to upstream's.
	CorrectedLabelID uint `gorm:"not null;index"`

	// Note is optional free text from the operator explaining the correction.
	Note string `gorm:"size:500"`

	CreatedAt time.Time `gorm:"autoCreateTime;index"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}

func (Correction) TableName() string { return "soundnet_corrections" }

// AllEntities returns every entity this package owns, in migration order.
func AllEntities() []any {
	return []any{&Diagnostics{}, &Enrichment{}, &Correction{}}
}
