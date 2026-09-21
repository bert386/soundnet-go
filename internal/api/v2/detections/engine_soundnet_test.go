package detections

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFilterByEngineUsesTheRowsOwnAircraft(t *testing.T) {
	t.Parallel()
	rows := []DetectionResponse{
		{ID: 1, Aircraft: &DetectionAircraft{Hex: "a", TypeCode: "B738"}},
		{ID: 2, Aircraft: &DetectionAircraft{Hex: "b", TypeCode: "C208"}},
		{ID: 3, Aircraft: &DetectionAircraft{Hex: "c", TypeCode: "A139"}},
		{ID: 4, Aircraft: &DetectionAircraft{Hex: "d"}},                   // identified, no type
		{ID: 5, Aircraft: &DetectionAircraft{Hex: "e", TypeCode: "ZZZZ"}}, // type not in the table
		{ID: 6}, // no ADS-B match
	}
	ids := func(engine string) []uint {
		var out []uint
		for _, r := range filterByEngine(rows, engine) {
			out = append(out, r.ID)
		}
		return out
	}
	assert.Equal(t, []uint{1}, ids("jet"))
	assert.Equal(t, []uint{2}, ids("prop"), "a Caravan is a prop")
	assert.Equal(t, []uint{3}, ids("helicopter"))
	assert.Equal(t, []uint{4, 5}, ids("other"), "identified but untyped or unmapped")
	assert.Equal(t, []uint{6}, ids("unidentified"))
	assert.Len(t, filterByEngine(rows, ""), 6, "no engine, no filtering")
}

func TestValidEngine(t *testing.T) {
	t.Parallel()
	for _, e := range []string{"jet", "prop", "helicopter", "other", "unidentified"} {
		assert.True(t, validEngine(e), e)
	}
	assert.False(t, validEngine("turboprop"), "the list filters on the coarse class")
	assert.False(t, validEngine("JET"))
}
