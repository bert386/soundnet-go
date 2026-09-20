package detections

// SOUNDNET: carry the corrected domain into the detection list.
//
// The list is where an operator actually reads detections, and until this it
// showed only the acoustic label. AudioSet's Vehicle is the parent class of
// Aircraft, so an airliner is recorded as road traffic; nineteen of nineteen
// reviewed Thunder detections at this station were passing jets. In every one
// of those cases ADS-B had already named the aircraft by the time the row was
// read, and nothing in the list said so.

import (
	"gorm.io/gorm"

	"github.com/bert386/soundnet-go/internal/eventclass"
	"github.com/bert386/soundnet-go/internal/eventrecord"
)

// annotateResolvedDomains fills in ResolvedDomain for any detection an authority
// settled under a different domain from the one the sound suggested.
//
// Failure is silent. This adds a field to a list that is already correct
// without it, so a database error here must cost the annotation, not the page.
func (c *Handler) annotateResolvedDomains(detections []DetectionResponse) {
	if c.DS == nil || len(detections) == 0 {
		return
	}

	// Only a class that does not determine its own domain can be corrected, and
	// only an event class is a candidate at all. Filtering here keeps the query
	// off every page of ordinary bird detections.
	ids := make([]uint, 0, len(detections))
	index := make(map[uint][]int, len(detections))
	for i := range detections {
		if detections[i].EventDisplayName == "" || !eventclass.IsAmbiguous(detections[i].ScientificName) {
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

	var resolved map[uint]string
	err := c.DS.Transaction(func(tx *gorm.DB) error {
		var rerr error
		resolved, rerr = eventrecord.NewStore(tx).ResolvedDomains(ids)
		return rerr
	})
	if err != nil {
		return
	}

	for id, domain := range resolved {
		for _, i := range index[id] {
			class, found := eventclass.Resolve(detections[i].ScientificName)
			// A resolution that agrees with the acoustic reading is not a
			// correction, and dressing it as one would teach an operator to
			// ignore the field on the occasions it matters.
			if domain != "" && found && domain != string(class.Domain) {
				detections[i].ResolvedDomain = domain
			}
		}
	}
}
