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
}

// soundNetDomainSummary is one event family over the period.
type soundNetDomainSummary struct {
	Domain string `json:"domain"`

	// Detections is rows, not events: one aeroplane crossing the sky produces
	// several. The pass grouping in the detections list is what collapses them,
	// and it needs per-detection data this summary does not carry.
	Detections int `json:"detections"`

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

		summary := byDomain[class.Domain]
		if summary == nil {
			summary = &soundNetDomainSummary{
				Domain:      string(class.Domain),
				Diagnosable: class.Domain.Diagnosable(),
				Enrichable:  class.Domain.Enrichable(),
			}
			byDomain[class.Domain] = summary
		}
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
		})
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

	// Aircraft identities live in this fork's own tables, so a missing or
	// unmigrated store costs the aircraft list rather than the overview.
	if c.DS != nil {
		var sightings []eventrecord.AircraftSighting
		if err := c.DS.Transaction(func(tx *gorm.DB) error {
			var rerr error
			sightings, rerr = eventrecord.NewStore(tx).AircraftSeen(from, to)
			return rerr
		}); err == nil {
			resp.Aircraft = sightings
		}
	}

	return ctx.JSON(http.StatusOK, resp)
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
