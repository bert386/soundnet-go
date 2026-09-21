package api

// The station's own overview, grouped by what made the sound.
//
// The analytics this fork inherited are built around a species list, which is
// right for what they were written for and wrong for everything else the
// station hears. On the operator's screen "Vehicle", "Thunderstorm",
// "aircraft_and_airplane" and "Purr" appear as birds, each with a bird
// silhouette where a photograph should be, inside a count of "45 species".
//
// So this answers a different question: over a period, what did the station
// hear, grouped by domain - and for aircraft, which ones specifically.
//
// Two numbers per domain, not one. A class says what a sound resembled; an
// authority says what it was, and at this station those disagree constantly:
// every reviewed Thunder detection here has been an aeroplane, and ADS-B has
// already named the aircraft on about half of them. Reporting only the
// acoustic reading files those aircraft under weather. Reporting only the
// settled one would hide that the classifier is wrong, which is the thing
// worth knowing. Both are reported: Heard is what the classes sum to,
// Detections is what the station concluded.

import (
	"net/http"
	"sort"
	"time"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	"github.com/bert386/soundnet-go/internal/eventclass"
	"github.com/bert386/soundnet-go/internal/eventrecord"
)

// soundNetClassCount is one class within a domain.
type soundNetClassCount struct {
	// Label is the taxonomy's name, never the stored one. The stored form is
	// truncated and mangled - "and_airscrew", "car_(siren)" - which is what the
	// species analytics page is currently showing an operator.
	Label string `json:"label"`

	Count         int     `json:"count"`
	MaxConfidence float64 `json:"maxConfidence,omitempty"`
	LastHeard     string  `json:"lastHeard,omitempty"`

	// AmbiguousWith names the domains this class could equally belong to, so a
	// count of 83 Thunder detections can carry the reason it should not be read
	// as 83 storms. Taken from the taxonomy's ambiguity table, which is also
	// what decides whether the detection is put to an authority at all.
	AmbiguousWith []string `json:"ambiguousWith,omitempty"`
}

// soundNetDomainMove is a count of detections an authority settled under a
// different domain from the one their class belongs to.
type soundNetDomainMove struct {
	Domain     string `json:"domain"`
	Detections int    `json:"detections"`
}

// soundNetDomainSummary is one event family over the period.
type soundNetDomainSummary struct {
	Domain string `json:"domain"`

	// Detections is what the station concluded: the classes heard here, less
	// the ones an authority moved away, plus the ones it moved in. Rows, not
	// events - one aeroplane crossing the sky produces several, and the pass
	// grouping in the detections list is what collapses them.
	Detections int `json:"detections"`

	// Heard is what the classes below sum to, before any identification. Kept
	// separate so the class list stays internally consistent and so the size of
	// the correction is visible rather than absorbed.
	Heard int `json:"heard"`

	// IdentifiedAs and IdentifiedFrom are the two halves of every move, seen
	// from each end: weather reports that 109 of its detections were identified
	// as aircraft, and aircraft reports that 109 of its were heard as weather.
	IdentifiedAs   []soundNetDomainMove `json:"identifiedAs,omitempty"`
	IdentifiedFrom []soundNetDomainMove `json:"identifiedFrom,omitempty"`

	Classes     []soundNetClassCount `json:"classes"`
	Diagnosable bool                 `json:"diagnosable"`
	Enrichable  bool                 `json:"enrichable"`
	LastHeard   string               `json:"lastHeard,omitempty"`
}

// soundNetOverviewResponse is the whole period at a glance.
type soundNetOverviewResponse struct {
	From string `json:"from"`
	To   string `json:"to"`

	Domains []soundNetDomainSummary `json:"domains"`

	// Aircraft is the part with no equivalent on the species side: the actual
	// machines, by registration, that flew over and were identified.
	Aircraft []eventrecord.AircraftSighting `json:"aircraft"`

	// BirdDetections is reported rather than hidden, so the events can be read
	// in proportion to everything else the station heard.
	BirdDetections int `json:"birdDetections"`
}

// GetSoundNetOverview handles GET /api/v2/soundnet/overview.
func (c *Controller) GetSoundNetOverview(ctx echo.Context) error {
	to := time.Now()
	from := to.AddDate(0, 0, -defaultOverviewDays)
	if raw := ctx.QueryParam("days"); raw != "" {
		if days := parsePositiveInt(raw, defaultOverviewDays); days > 0 {
			from = to.AddDate(0, 0, -days)
		}
	}

	// A single day, for the dashboard, which navigates by date rather than by
	// period. Parsed in the station's own zone: an operator looking at
	// yesterday means the station's yesterday, not UTC's, and this station is
	// ten hours from it. A date that will not parse is ignored rather than
	// refused - the period it falls back to is still a true answer.
	if raw := ctx.QueryParam("date"); raw != "" {
		if day, err := time.ParseInLocation(time.DateOnly, raw, time.Local); err == nil {
			from = day
			to = day.AddDate(0, 0, 1).Add(-time.Nanosecond)
		}
	}

	rows, err := c.DS.GetSpeciesSummaryData(ctx.Request().Context(),
		from.Format(time.DateOnly), to.Format(time.DateOnly))
	if err != nil {
		return c.HandleError(ctx, err, "Failed to summarise detections", http.StatusInternalServerError)
	}

	resp := soundNetOverviewResponse{
		From:     from.Format(time.RFC3339),
		To:       to.Format(time.RFC3339),
		Aircraft: []eventrecord.AircraftSighting{},
		Domains:  []soundNetDomainSummary{},
	}

	byDomain := make(map[eventclass.Domain]*soundNetDomainSummary)
	for i := range rows {
		row := &rows[i]
		class, found := eventclass.Resolve(row.ScientificName)
		if !found {
			// Everything the taxonomy does not map is a bird, or something the
			// station was never asked to name. Counted, not listed.
			resp.BirdDetections += row.Count
			continue
		}

		summary := domainSummaryFor(byDomain, class.Domain)
		summary.Heard += row.Count
		summary.Detections += row.Count

		last := row.LastSeen.Format(time.RFC3339)
		if last > summary.LastHeard {
			summary.LastHeard = last
		}
		summary.Classes = append(summary.Classes, soundNetClassCount{
			Label:         class.Label,
			Count:         row.Count,
			MaxConfidence: row.MaxConfidence,
			LastHeard:     last,
			AmbiguousWith: otherCandidateDomains(class),
		})
	}

	// Aircraft identities and domain corrections both live in this fork's own
	// tables, so a missing or unmigrated store costs them rather than the
	// overview: the acoustic reading on its own is still worth showing.
	if c.DS != nil {
		var sightings []eventrecord.AircraftSighting
		var moves []eventrecord.DomainMove
		if err := c.DS.Transaction(func(tx *gorm.DB) error {
			store := eventrecord.NewStore(tx)
			var rerr error
			if sightings, rerr = store.AircraftSeen(from, to); rerr != nil {
				return rerr
			}
			moves, rerr = store.DomainReassignments(from, to)
			return rerr
		}); err == nil {
			resp.Aircraft = sightings
			applyDomainMoves(byDomain, moves)
		}
	}

	for _, summary := range byDomain {
		sort.Slice(summary.Classes, func(i, j int) bool {
			return summary.Classes[i].Count > summary.Classes[j].Count
		})
		resp.Domains = append(resp.Domains, *summary)
	}
	sort.Slice(resp.Domains, func(i, j int) bool {
		return resp.Domains[i].Detections > resp.Domains[j].Detections
	})

	return ctx.JSON(http.StatusOK, resp)
}

// domainSummaryFor returns the summary for a domain, creating it on first use.
//
// A domain can be reached two ways - by a class that belongs to it, or by an
// authority moving a detection into it - and the second must work even when the
// station heard nothing of its own. A station whose only aircraft evidence is
// ADS-B on misheard thunder still has aircraft.
func domainSummaryFor(
	byDomain map[eventclass.Domain]*soundNetDomainSummary,
	domain eventclass.Domain,
) *soundNetDomainSummary {
	summary := byDomain[domain]
	if summary == nil {
		summary = &soundNetDomainSummary{
			Domain:      string(domain),
			Diagnosable: domain.Diagnosable(),
			Enrichable:  domain.Enrichable(),
			Classes:     []soundNetClassCount{},
		}
		byDomain[domain] = summary
	}
	return summary
}

// applyDomainMoves rebases each domain's count on what an authority settled.
//
// Only Detections moves. Heard and the class list stay where the classifier put
// them, because they answer a different question and an operator tuning a
// threshold needs the acoustic reading intact.
func applyDomainMoves(
	byDomain map[eventclass.Domain]*soundNetDomainSummary,
	moves []eventrecord.DomainMove,
) {
	for _, move := range moves {
		fromDomain, fromOK := eventclass.ParseDomain(move.From)
		toDomain, toOK := eventclass.ParseDomain(move.To)
		if !fromOK || !toOK || fromDomain == toDomain {
			// A domain this build does not know is one whose name changed under
			// a row written by an older binary. Skipping it loses a correction;
			// guessing would move detections into a domain that is not there.
			continue
		}

		// A move can only take away detections the summary actually has. The
		// enrichment window and the summary's date window are not the same
		// query, so an edge row can be in one and not the other, and an
		// overview that reported negative weather would be worse than one that
		// under-corrects.
		source := domainSummaryFor(byDomain, fromDomain)
		moved := move.Detections
		if moved > source.Detections {
			moved = source.Detections
		}
		if moved <= 0 {
			continue
		}

		source.Detections -= moved
		source.IdentifiedAs = appendMove(source.IdentifiedAs, string(toDomain), moved)

		target := domainSummaryFor(byDomain, toDomain)
		target.Detections += moved
		target.IdentifiedFrom = appendMove(target.IdentifiedFrom, string(fromDomain), moved)
	}
}

// appendMove adds to an existing entry for the domain rather than listing it
// twice, so two providers moving detections the same way read as one fact.
func appendMove(moves []soundNetDomainMove, domain string, count int) []soundNetDomainMove {
	for i := range moves {
		if moves[i].Domain == domain {
			moves[i].Detections += count
			return moves
		}
	}
	return append(moves, soundNetDomainMove{Domain: domain, Detections: count})
}

// otherCandidateDomains lists the domains a class could belong to besides its
// own, or nothing when the taxonomy considers it settled.
func otherCandidateDomains(class eventclass.Class) []string {
	candidates := class.CandidateDomains()
	if len(candidates) < 2 {
		return nil
	}
	out := make([]string, 0, len(candidates)-1)
	for _, domain := range candidates[1:] {
		out = append(out, string(domain))
	}
	return out
}

// defaultOverviewDays is the period the overview covers when none is asked for.
const defaultOverviewDays = 7

// parsePositiveInt reads a positive integer, falling back rather than failing:
// a malformed period is not worth refusing a page over.
func parsePositiveInt(raw string, fallback int) int {
	n := 0
	for _, r := range raw {
		if r < '0' || r > '9' {
			return fallback
		}
		n = n*10 + int(r-'0')
		if n > 3650 {
			return 3650
		}
	}
	if n == 0 {
		return fallback
	}
	return n
}
