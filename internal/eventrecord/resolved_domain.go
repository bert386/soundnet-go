package eventrecord

import (
	"encoding/json"
	"fmt"
)

// resolvedDomainKey is where the pipeline records the domain an identity came
// back under, inside the provider's own document. It is written by
// eventpipeline.withQueriedDomain.
const resolvedDomainKey = "soundnetResolvedDomain"

// identityKey is the attribute a provider is authoritative about. ADS-B
// broadcasts a hex code; a lightning network would have its own.
const identityKey = "hex"

// EnrichmentSummary is what a list view needs from an enrichment without
// parsing a provider-specific document.
type EnrichmentSummary struct {
	// ResolvedDomain is set only when an authority answered under a different
	// domain from the one the class belongs to.
	ResolvedDomain string

	// Identity is the thing the authority named. Two detections naming the same
	// identity are the same source heard twice, which is the only signal that
	// can contradict timing when grouping a pass - and when it does, it wins.
	Identity string
}

// EnrichmentSummaries reads both facts for a page of detections in one query.
//
// One query rather than two, and one for the page rather than one per row: this
// answers a question the detection list asks about fifty rows at a time, and
// the cost of asking has to stay well under the value of the answer.
func (s *Store) EnrichmentSummaries(detectionIDs []uint) (map[uint]EnrichmentSummary, error) {
	if len(detectionIDs) == 0 {
		return map[uint]EnrichmentSummary{}, nil
	}

	var rows []Enrichment
	if err := s.db.Where("detection_id IN ?", detectionIDs).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("eventrecord: read enrichments: %w", err)
	}

	out := make(map[uint]EnrichmentSummary, len(rows))
	for i := range rows {
		var attrs map[string]any
		if err := json.Unmarshal(rows[i].Payload, &attrs); err != nil {
			// A provider document that will not parse costs this one row its
			// annotation. Failing the whole page over it would take a detection
			// list away from an operator to report a field they never asked for.
			continue
		}
		summary := out[rows[i].DetectionID]
		if domain, ok := attrs[resolvedDomainKey].(string); ok && domain != "" {
			summary.ResolvedDomain = domain
		}
		if identity, ok := attrs[identityKey].(string); ok && identity != "" {
			summary.Identity = identity
		}
		out[rows[i].DetectionID] = summary
	}
	return out, nil
}

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

	summaries, err := s.EnrichmentSummaries(detectionIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[uint]string, len(summaries))
	for id, summary := range summaries {
		if summary.ResolvedDomain != "" {
			out[id] = summary.ResolvedDomain
		}
	}
	return out, nil
}
