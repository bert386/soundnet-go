package aircrafttype

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every row must be internally consistent. A turboprop filed as coarse "jet"
// would be a wrong training label that nothing downstream could detect.
func TestTableIsConsistent(t *testing.T) {
	t.Parallel()
	rows, err := All()
	require.NoError(t, err)
	require.NotEmpty(t, rows)

	want := map[Engine]Coarse{
		EngineJet:        CoarseJet,
		EngineTurboprop:  CoarseProp,
		EnginePiston:     CoarseProp,
		EngineHelicopter: CoarseHelicopter,
	}
	for _, r := range rows {
		coarse, known := want[r.Engine]
		if !assert.True(t, known, "%s: unknown engine %q", r.Designator, r.Engine) {
			continue
		}
		assert.Equal(t, coarse, r.Coarse, "%s: %s must be coarse %s", r.Designator, r.Engine, coarse)
		assert.NotEmpty(t, r.Name, "%s has no name", r.Designator)
	}
}

// The types the deployment station had heard when the corpus was first built.
// If one of these stops resolving, the backfill silently loses its examples.
func TestStationTypesAreMapped(t *testing.T) {
	t.Parallel()
	for _, d := range []string{
		"B738", "P28A", "A320", "DA40", "A388", "A139", "B77W", "B789", "C208",
		"A21N", "A333", "B737", "A321", "SF34", "BCS3", "A359", "LEG2", "B38M",
		"A332", "DH8D", "P68", "B788", "PC24", "C152", "RV7",
	} {
		_, ok := Lookup(d)
		assert.True(t, ok, "%s is not in the table", d)
	}
}

func TestLookup(t *testing.T) {
	t.Parallel()
	got, ok := Lookup(" b738 ")
	require.True(t, ok, "lookup is case- and space-insensitive")
	assert.Equal(t, CoarseJet, got.Coarse)

	got, ok = Lookup("C208")
	require.True(t, ok)
	assert.Equal(t, EngineTurboprop, got.Engine)
	assert.Equal(t, CoarseProp, got.Coarse, "a turboprop is a prop to the microphone")

	got, ok = Lookup("A139")
	require.True(t, ok)
	assert.Equal(t, CoarseHelicopter, got.Coarse)

	_, ok = Lookup("ZZZZ")
	assert.False(t, ok, "an unknown designator is reported, never guessed")
	_, ok = Lookup("")
	assert.False(t, ok)
}

func TestParseRejectsBadTables(t *testing.T) {
	t.Parallel()
	_, err := parse("type,engine,coarse,name\nB738,jet,jet\n")
	assert.Error(t, err, "short row")
	_, err = parse("type,engine,coarse,name\nB738,jet,jet,a\nB738,jet,jet,b\n")
	assert.Error(t, err, "duplicate designator")
}
