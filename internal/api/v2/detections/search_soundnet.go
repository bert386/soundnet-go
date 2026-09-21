package detections

// SOUNDNET: the domain correction and the aircraft, carried into search results.
//
// The detections list has shown both for a while; search did not, so an
// aeroplane recorded as Thunderstorm was a thunderstorm again as soon as it was
// found by searching rather than browsing. One batch lookup per page, and a
// failure costs the annotation, never the results.

import (
	"strconv"

	"gorm.io/gorm"

	"github.com/bert386/soundnet-go/internal/datastore"
	"github.com/bert386/soundnet-go/internal/eventrecord"
)

func (c *Handler) annotateSearchResults(results []datastore.DetectionRecord) {
	if c.DS == nil || len(results) == 0 {
		return
	}
	ids := make([]uint, 0, len(results))
	index := make(map[uint][]int, len(results))
	for i := range results {
		if results[i].EventDisplayName == "" {
			// A bird. Nothing in SoundNet's tables can be about it.
			continue
		}
		id, err := strconv.ParseUint(results[i].ID, 10, 64)
		if err != nil {
			continue
		}
		if _, seen := index[uint(id)]; !seen {
			ids = append(ids, uint(id))
		}
		index[uint(id)] = append(index[uint(id)], i)
	}
	if len(ids) == 0 {
		return
	}

	var summaries map[uint]eventrecord.EnrichmentSummary
	if err := c.DS.Transaction(func(tx *gorm.DB) error {
		var rerr error
		summaries, rerr = eventrecord.NewStore(tx).EnrichmentSummaries(ids)
		return rerr
	}); err != nil {
		return
	}

	for id, summary := range summaries {
		var aircraft *datastore.EventAircraft
		if summary.Identity != "" {
			aircraft = &datastore.EventAircraft{
				Hex:          summary.Identity,
				Registration: summary.Registration,
				TypeCode:     summary.TypeCode,
				TypeName:     summary.TypeName,
				Operator:     summary.Operator,
				Callsign:     summary.Callsign,
			}
		}
		for _, pos := range index[id] {
			results[pos].ResolvedDomain = summary.ResolvedDomain
			results[pos].Aircraft = aircraft
		}
	}
}
