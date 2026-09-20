package detections

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bert386/soundnet-go/internal/api/v2/apicore"
	"github.com/bert386/soundnet-go/internal/api/v2/apitest"
	"github.com/bert386/soundnet-go/internal/datastore"
)

// TestNoteToDetectionResponse_EventDisplayName covers the wiring rather than the
// name resolution, which internal/eventclass tests against the real upstream
// label parser.
//
// It exists because the resolution being right is not the part that has gone
// wrong on this project. The category filter passed every unit test while being
// a complete no-op in production, because the test exercised the function and
// not the path that reaches it. So this asserts the field arrives on the
// response an operator actually receives, and that a bird leaves it empty.
func TestNoteToDetectionResponse_EventDisplayName(t *testing.T) {
	t.Parallel()
	t.Attr("component", "detections")
	t.Attr("type", "unit")
	t.Attr("feature", "soundnet-display-names")

	controller := &Handler{Core: &apicore.Core{}}
	controller.Settings.Store(apitest.NewValidTestSettings())

	tests := []struct {
		name       string
		scientific string
		common     string
		want       string
	}{
		{
			// The case the operator reported, in the exact form the station
			// stores it: id=805 and id=828 are both this pair.
			name:       "aircraft shown as and_airscrew recovers its full name",
			scientific: "propeller",
			common:     "and_airscrew",
			want:       "Propeller, airscrew",
		},
		{
			// Also read off the station. Parentheses and a second underscore in
			// one label, which is the shape that breaks naive reconstruction.
			name:       "siren with punctuation",
			scientific: "police",
			common:     "car_(siren)",
			want:       "Police car (siren)",
		},
		{
			name:       "two-token event class",
			scientific: "jet",
			common:     "engine",
			want:       "Jet engine",
		},
		{
			// A label with no underscore to split on: the resolver supplies the
			// display form, so the pair differs only by case. Single-word classes
			// were always displayed correctly, which is precisely why the
			// breakage went unnoticed - pinned so a fix for the multi-word case
			// cannot quietly break this one.
			name:       "class with no separator in its label",
			scientific: "vehicle",
			common:     "Vehicle",
			want:       "Vehicle",
		},
		{
			name:       "bird species carries no event display name",
			scientific: "Trichoglossus moluccanus",
			common:     "Rainbow Lorikeet",
			want:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			note := &datastore.Note{
				ID:             7,
				Date:           "2026-09-20",
				Time:           "10:51:16",
				ScientificName: tt.scientific,
				CommonName:     tt.common,
			}
			resp := controller.noteToDetectionResponse(note, false, nil)

			assert.Equal(t, tt.want, resp.EventDisplayName)
			// The identity fields must survive untouched: CommonName is the key
			// the species exclude list is matched on, so rewriting it would make
			// "ignore this species" stop working for exactly these detections.
			assert.Equal(t, tt.scientific, resp.ScientificName)
			assert.Equal(t, tt.common, resp.CommonName)
		})
	}
}
