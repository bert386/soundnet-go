package eventrecord

import (
	"encoding/json"
	"fmt"
)

// resolvedDomainKey is where the pipeline records the domain an identity came
// back under, inside the provider's own document. It is written by
// eventpipeline.withQueriedDomain.
const resolvedDomainKey = "soundnetResolvedDomain"

// ResolvedDomains reports, for each of the given detections, the domain an
// authority settled it under when that differs from the acoustic reading.
//
// A batch lookup rather than one query per row. This answers a question the
// detection list asks about a whole page at once, and fifty queries to decide
// whether fifty rows are mislabelled would make showing the correction cost
// more than the correction is worth.
//
// Detections with no correction are simply absent from the map, which is the
// same answer as an unenriched detection and needs no separate signal: in both
// cases the acoustic label stands.
func (s *Store) ResolvedDomains(detectionIDs []uint) (map[uint]string, error) {
	if len(detectionIDs) == 0 {
		// An empty map rather than nil: a page with no ambiguous classes is an
		// ordinary answer, not an absence a caller has to test for separately.
		return map[uint]string{}, nil
	}

	var rows []Enrichment
	if err := s.db.Where("detection_id IN ?", detectionIDs).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("eventrecord: read enrichments: %w", err)
	}

	out := make(map[uint]string, len(rows))
	for i := range rows {
		var attrs map[string]any
		if err := json.Unmarshal(rows[i].Payload, &attrs); err != nil {
			// A provider document that will not parse costs this one row its
			// correction. Failing the whole page over it would take a detection
			// list away from an operator to report a field they never asked for.
			continue
		}
		if domain, ok := attrs[resolvedDomainKey].(string); ok && domain != "" {
			out[rows[i].DetectionID] = domain
		}
	}
	return out, nil
}
