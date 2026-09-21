package detections

// SOUNDNET: filtering an aircraft list by engine class.
//
// The dashboard's aircraft grid splits the day into jet, prop and helicopter
// from the type code ADS-B returned, and each cell links here. The list has to
// apply the identical rule or the cell and the page it opens will disagree -
// which is exactly what the operator noticed the first time a cell said 130
// and the list said 176.
//
// So a row's engine is its own aircraft's, never its pass's. A window inside a
// known jet's pass that was not itself matched is "unidentified" here and in
// the grid alike. In practice most windows of a pass are matched individually.

import (
	"github.com/bert386/soundnet-go/internal/aircrafttype"
)

const (
	engineOther        = "other"
	engineUnidentified = "unidentified"
)

func validEngine(e string) bool {
	switch e {
	case string(aircrafttype.CoarseJet), string(aircrafttype.CoarseProp),
		string(aircrafttype.CoarseHelicopter), engineOther, engineUnidentified:
		return true
	}
	return false
}

// engineOf is the engine class a row is counted under.
func engineOf(d *DetectionResponse) string {
	if d.Aircraft == nil {
		return engineUnidentified
	}
	if t, ok := aircrafttype.Lookup(d.Aircraft.TypeCode); ok {
		return string(t.Coarse)
	}
	return engineOther
}

func filterByEngine(detections []DetectionResponse, engine string) []DetectionResponse {
	if engine == "" {
		return detections
	}
	kept := make([]DetectionResponse, 0, len(detections))
	for i := range detections {
		if engineOf(&detections[i]) == engine {
			kept = append(kept, detections[i])
		}
	}
	return kept
}
