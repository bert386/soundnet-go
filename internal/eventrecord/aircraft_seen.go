package eventrecord

// Which aircraft the station has actually identified, over a period.
//
// The detections table knows a sound was an aircraft; only the enrichment rows
// know which one. Reading them back the other way round - by aircraft rather
// than by detection - is what turns a list of "Aircraft 0.24" rows into
// something an operator recognises: a Cessna that goes over every morning, a
// rescue helicopter that came twice in a night.

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"
)

// AircraftSighting is one aircraft and everything the station learned about it
// in the period.
type AircraftSighting struct {
	// Hex is the ICAO24 address the aircraft broadcasts, and the only
	// identifier here that comes from the aircraft itself rather than a lookup.
	Hex string `json:"hex"`

	Registration string `json:"registration,omitempty"`
	TypeCode     string `json:"typeCode,omitempty"`
	TypeName     string `json:"typeName,omitempty"`
	Operator     string `json:"operator,omitempty"`
	Callsign     string `json:"callsign,omitempty"`

	// Detections counts the rows this aircraft was named on, which is more than
	// the number of times it flew over: one pass identifies several.
	Detections int `json:"detections"`

	// ClosestKm is the nearest slant range recorded, which is the best measure
	// of how well it was heard.
	ClosestKm float64 `json:"closestKm,omitempty"`

	FirstSeen time.Time `json:"firstSeen"`
	LastSeen  time.Time `json:"lastSeen"`

	// Sources names the services that identified it, because two networks with
	// different coverage will not always agree and a match that looks wrong
	// later cannot be judged without knowing who said it.
	Sources []string `json:"sources,omitempty"`
}

// AircraftSeen returns every aircraft identified between from and to, most
// recently seen first.
func (s *Store) AircraftSeen(from, to time.Time) ([]AircraftSighting, error) {
	var rows []Enrichment
	err := s.db.
		Where("provider = ? AND created_at >= ? AND created_at <= ?", "adsb", from, to).
		Order("created_at asc").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("eventrecord: read aircraft sightings: %w", err)
	}

	byHex := make(map[string]*AircraftSighting)
	sourcesSeen := make(map[string]map[string]struct{})

	for i := range rows {
		var attrs map[string]any
		if err := json.Unmarshal(rows[i].Payload, &attrs); err != nil {
			// One unreadable provider document costs its own row, not the page.
			continue
		}
		hex, _ := attrs[identityKey].(string)
		if hex == "" {
			// Without the broadcast identifier there is nothing to group by.
			// Grouping on a looked-up registration instead would merge two
			// aircraft whose lookups both failed.
			continue
		}

		sighting := byHex[hex]
		if sighting == nil {
			sighting = &AircraftSighting{Hex: hex, FirstSeen: rows[i].CreatedAt}
			byHex[hex] = sighting
			sourcesSeen[hex] = make(map[string]struct{})
		}
		sighting.Detections++
		sighting.LastSeen = rows[i].CreatedAt

		// Later rows fill in what earlier ones lacked: the metadata lookup is
		// best-effort, so the same aircraft may be named on one pass and not
		// the next.
		fill(&sighting.Registration, attrs, "registration")
		fill(&sighting.TypeCode, attrs, "type_code")
		fill(&sighting.TypeName, attrs, "type_name")
		fill(&sighting.Operator, attrs, "operator")
		fill(&sighting.Callsign, attrs, "callsign")

		if km, ok := attrs["slant_range_km"].(float64); ok && km > 0 {
			if sighting.ClosestKm == 0 || km < sighting.ClosestKm {
				sighting.ClosestKm = km
			}
		}
		if src := rows[i].Source; src != "" {
			sourcesSeen[hex][src] = struct{}{}
		}
	}

	out := make([]AircraftSighting, 0, len(byHex))
	for hex, sighting := range byHex {
		sighting.Sources = slices.Collect(maps.Keys(sourcesSeen[hex]))
		slices.Sort(sighting.Sources)
		out = append(out, *sighting)
	}
	slices.SortFunc(out, func(a, b AircraftSighting) int { return b.LastSeen.Compare(a.LastSeen) })
	return out, nil
}

// HourlyType is how many identified detections of one aircraft type fell in
// one station-local hour. TypeCode is empty when the lookup behind it found
// nothing - the aircraft was identified, its type was not.
type HourlyType struct {
	Hour       int
	TypeCode   string
	Detections int
}

// HourlyAircraftTypes counts the day's identified detections by hour and type.
//
// The hour is the enrichment row's own timestamp, written as the detection is
// saved, the same approximation HourlyDomainReassignments makes and for the same
// reason: no portable join to the detections table exists.
func (s *Store) HourlyAircraftTypes(from, to time.Time) ([]HourlyType, error) {
	var rows []Enrichment
	err := s.db.
		Where("provider = ? AND created_at >= ? AND created_at <= ?", "adsb", from, to).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("eventrecord: read hourly aircraft types: %w", err)
	}

	type key struct {
		hour int
		code string
	}
	counts := make(map[key]int)
	for i := range rows {
		var attrs map[string]any
		if err := json.Unmarshal(rows[i].Payload, &attrs); err != nil {
			continue
		}
		code, _ := attrs["type_code"].(string)
		counts[key{hour: rows[i].CreatedAt.Local().Hour(), code: code}]++
	}

	out := make([]HourlyType, 0, len(counts))
	for k, n := range counts {
		out = append(out, HourlyType{Hour: k.hour, TypeCode: k.code, Detections: n})
	}
	return out, nil
}

// fill sets dst from the attribute when dst is still empty, so the first
// provider to know something is not overwritten by a later one that does not.
func fill(dst *string, attrs map[string]any, key string) {
	if *dst != "" {
		return
	}
	if v, ok := attrs[key].(string); ok && v != "" {
		*dst = v
	}
}
