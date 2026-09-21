package detections

// SOUNDNET: carry the corrected domain and the pass grouping into the list.
//
// The list is where an operator reads detections, and on its own it misleads
// twice over. AudioSet's Vehicle is the parent class of Aircraft, so an
// airliner is recorded as road traffic; and one aeroplane crossing the sky
// makes three to six rows over half a minute under whatever class each model
// reached for in each window. A real morning:
//
//	06:39:56  Vehicle                0.74   -> aircraft  8a047a
//	06:39:57  Fixed-wing aircraft    0.26             8a047a
//	06:40:10  Fixed-wing aircraft    0.23             8a047a
//	06:40:16  Vehicle                0.34   -> aircraft  8a047a
//	06:40:46  Fixed-wing aircraft    0.20             8a047a
//	06:40:48  Vehicle                0.47   -> aircraft  8a047a
//
// Six rows, one aeroplane. Thirteen such groups that morning, every one naming
// exactly one aircraft and none naming two.
//
// Both annotations come from one query, and nothing is discarded: each row is a
// model's opinion about a window, and those opinions are the training corpus.

import (
	"time"

	"gorm.io/gorm"

	"github.com/bert386/soundnet-go/internal/eventclass"
	"github.com/bert386/soundnet-go/internal/eventpass"
	"github.com/bert386/soundnet-go/internal/eventrecord"
)

// annotateSoundNet fills in ResolvedDomain and PassID.
//
// Failure is silent. These add fields to a list that is already correct without
// them, so a database error must cost the annotation, not the page.
func (c *Handler) annotateSoundNet(detections []DetectionResponse) {
	if c.DS == nil || len(detections) == 0 {
		return
	}

	// Every event class is a candidate for grouping, not only the ambiguous
	// ones: an `Aircraft` row is part of the pass even though its own class
	// settles its domain. Birds are excluded, which keeps the query off every
	// page of ordinary detections.
	ids := make([]uint, 0, len(detections))
	index := make(map[uint][]int, len(detections))
	for i := range detections {
		if detections[i].EventDisplayName == "" {
			continue
		}
		id := detections[i].ID
		if _, seen := index[id]; !seen {
			ids = append(ids, id)
		}
		index[id] = append(index[id], i)
	}
	if len(ids) == 0 {
		return
	}

	var summaries map[uint]eventrecord.EnrichmentSummary
	err := c.DS.Transaction(func(tx *gorm.DB) error {
		var rerr error
		summaries, rerr = eventrecord.NewStore(tx).EnrichmentSummaries(ids)
		return rerr
	})
	if err != nil {
		return
	}

	grouping := make([]eventpass.Detection, 0, len(ids))
	for id, positions := range index {
		i := positions[0]
		class, found := eventclass.Resolve(detections[i].ScientificName)
		if !found {
			continue
		}
		summary := summaries[id]

		// The aircraft itself, on every row it was named on. Rows inside the
		// same pass that nobody identified stay bare: the pass says they belong
		// together, and claiming the registration for all of them would turn a
		// grouping into an assertion about each sound.
		if summary.Identity != "" {
			aircraft := &DetectionAircraft{
				Hex:          summary.Identity,
				Registration: summary.Registration,
				TypeCode:     summary.TypeCode,
				TypeName:     summary.TypeName,
				Operator:     summary.Operator,
				Callsign:     summary.Callsign,
			}
			for _, pos := range positions {
				detections[pos].Aircraft = aircraft
			}
		}

		// The resolved domain when an authority gave one, the class's own
		// otherwise. Grouping on the class name alone would scatter one
		// aeroplane across three domains, which is the problem, not the fix.
		domain := string(class.Domain)
		if summary.ResolvedDomain != "" {
			if summary.ResolvedDomain != string(class.Domain) {
				for _, pos := range positions {
					detections[pos].ResolvedDomain = summary.ResolvedDomain
				}
			}
			domain = summary.ResolvedDomain
		}

		at, ok := detectionTime(&detections[i])
		if !ok {
			continue
		}
		grouping = append(grouping, eventpass.Detection{
			ID: id, At: at, Domain: domain, Identity: summary.Identity,
			Candidates: candidateDomains(class),
		})
	}

	// A pass that is only one detection is not worth telling a client about;
	// leaving PassID unset keeps the field honest as "this belongs with others".
	sizes := make(map[uint]int)
	passes := eventpass.PassIDs(grouping, eventpass.DefaultGap)
	for _, passID := range passes {
		sizes[passID]++
	}
	for id, passID := range passes {
		if sizes[passID] < 2 {
			continue
		}
		for _, pos := range index[id] {
			detections[pos].PassID = passID
		}
	}
}

// candidateDomains is the taxonomy's ambiguity table in the form the grouping
// wants: every domain this class could belong to, its own first.
//
// It is what lets an unidentified Thunderstorm join the aeroplane it was
// recorded beside. Without it the same flight appears three times in the list -
// once under aircraft, once under vehicle and once under weather - which is
// what an operator reported seeing for QF642.
func candidateDomains(class eventclass.Class) []string {
	candidates := class.CandidateDomains()
	out := make([]string, 0, len(candidates))
	for _, domain := range candidates {
		out = append(out, string(domain))
	}
	return out
}

// detectionTime recovers when a detection was heard.
//
// Timestamp is built from the note's date and time by the response builder and
// is the same clock the grouping needs; Date and Time are the fallback for the
// case where that parse failed, since a detection with no usable time cannot be
// grouped at all and should be left alone rather than guessed at.
func detectionTime(d *DetectionResponse) (time.Time, bool) {
	if d.Timestamp != "" {
		if t, err := time.Parse(time.RFC3339, d.Timestamp); err == nil {
			return t, true
		}
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", d.Date+" "+d.Time, time.Local); err == nil {
		return t, true
	}
	return time.Time{}, false
}
