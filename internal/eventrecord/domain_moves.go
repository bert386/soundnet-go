package eventrecord

// Where an authority moved a detection to, counted over a period.
//
// The station files a detection under the domain its class belongs to, and at
// this station that is wrong more often than it is right for one family of
// labels: every Thunder and Thunderstorm detection an operator has reviewed
// here - fifty of them - turned out to be an aeroplane. ADS-B already says so
// on the rows it could answer for, and the detections list already shows the
// correction. The overview did not, so the same aircraft appeared under
// weather in the one place that is meant to say what the station has been
// hearing.
//
// This reads the correction back the way the overview needs it: not per
// detection, but as a count of how many rows moved from one domain to another.
// Deliberately built from the enrichment documents alone. They already record
// both halves of the move, so no join to the detections table is needed - and
// that matters, because the two datastores this fork must work with do not
// agree on whether such a table exists.

import (
	"encoding/json"
	"fmt"
	"time"
)

// classifiedDomainKey is the domain the acoustic class belongs to, written
// beside resolvedDomainKey by eventpipeline.withQueriedDomain. Both are present
// or neither is: the pipeline writes the pair only when they differ.
const classifiedDomainKey = "soundnetClassifiedDomain"

// DomainMove is one authority-settled reassignment and how many detections it
// accounts for.
type DomainMove struct {
	// From is the domain the acoustic class belongs to - what the station
	// heard.
	From string

	// To is the domain the authority settled it under - what it was.
	To string

	// Detections counts rows, not passes. One aircraft crossing the sky
	// produces several, the same way it does everywhere else in this overview.
	Detections int
}

// DomainReassignments returns every move an authority made in the period.
//
// Rows where the authority agreed with the acoustic class are absent, because
// the pipeline does not write the pair for them. That is the same answer as an
// unenriched detection and needs no separate signal: in both cases the class
// stands.
func (s *Store) DomainReassignments(from, to time.Time) ([]DomainMove, error) {
	var rows []Enrichment
	err := s.db.
		Where("created_at >= ? AND created_at <= ?", from, to).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("eventrecord: read domain reassignments: %w", err)
	}

	// Keyed on the pair rather than nested maps: a move is one fact with two
	// halves, and the caller wants it back as a list either way.
	counts := make(map[[2]string]int)
	for i := range rows {
		var attrs map[string]any
		if err := json.Unmarshal(rows[i].Payload, &attrs); err != nil {
			// One unreadable provider document costs its own row, not the page.
			continue
		}
		classified, _ := attrs[classifiedDomainKey].(string)
		resolved, _ := attrs[resolvedDomainKey].(string)
		if classified == "" || resolved == "" || classified == resolved {
			continue
		}
		counts[[2]string{classified, resolved}]++
	}

	out := make([]DomainMove, 0, len(counts))
	for pair, n := range counts {
		out = append(out, DomainMove{From: pair[0], To: pair[1], Detections: n})
	}
	return out, nil
}
