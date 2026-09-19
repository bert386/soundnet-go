package enrichment_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bert386/soundnet-go/internal/enrichment"
)

func TestParseDecimalCoordinate(t *testing.T) {
	t.Parallel()

	// The real station, as the mapping site hands it out.
	v, err := enrichment.ParseCoordinate("-34.11159024409095")
	require.NoError(t, err)
	assert.InDelta(t, -34.11159024409095, v, 1e-12)

	v, err = enrichment.ParseCoordinate("  150.7922555571461  ")
	require.NoError(t, err)
	assert.InDelta(t, 150.7922555571461, v, 1e-12)
}

func TestParseDMSVariants(t *testing.T) {
	t.Parallel()

	// -34.11159 degrees is 34 deg 06 min 41.7 sec south. Operators copy this
	// form off charts and GPS units, in whatever punctuation their keyboard
	// offers, so all of these must resolve to the same place.
	for _, in := range []string{
		`34°06'41.7"S`,
		"34 06 41.7 S",
		"34:06:41.7S",
	} {
		got, err := enrichment.ParseCoordinate(in)
		require.NoError(t, err, in)
		assert.InDelta(t, -34.11158, got, 0.001, in)
	}
}

func TestHemisphereBeatsSign(t *testing.T) {
	t.Parallel()

	// "34 06 41.7 S" and "-34 06 41.7" both mean southern. Returning a northern
	// latitude for the first would be silently, confidently wrong - and would
	// put the station on the wrong side of the equator without any error.
	south, err := enrichment.ParseCoordinate("34 06 41.7 S")
	require.NoError(t, err)
	assert.Negative(t, south)

	alsoSouth, err := enrichment.ParseCoordinate("-34 06 41.7")
	require.NoError(t, err)
	assert.Negative(t, alsoSouth)
	assert.InDelta(t, south, alsoSouth, 1e-6)

	west, err := enrichment.ParseCoordinate(`150°47'32.1"W`)
	require.NoError(t, err)
	assert.Negative(t, west)
}

func TestParseRejectsNonsense(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"", "   ", "north-ish", "34 61 00 S", "34 00 75 S"} {
		_, err := enrichment.ParseCoordinate(in)
		assert.Error(t, err, "%q should not parse", in)
	}
}

func TestFormatDMSRoundTrips(t *testing.T) {
	t.Parallel()

	// The settings page shows both forms so an operator can check against a
	// chart, which is only useful if the displayed form means the same thing.
	formatted := enrichment.FormatDMS(-34.11159024409095, true)
	assert.Contains(t, formatted, "S")

	back, err := enrichment.ParseCoordinate(formatted)
	require.NoError(t, err)
	assert.InDelta(t, -34.11159024409095, back, 0.0001)
}

func TestResolveRealStation(t *testing.T) {
	t.Parallel()

	cfg := enrichment.StationConfig{
		Latitude:   "-34.11159024409095",
		Longitude:  "150.7922555571461",
		ElevationM: 140,
	}
	got, err := cfg.Resolve()
	require.NoError(t, err)
	assert.InDelta(t, -34.11159024409095, got.Latitude, 1e-12)
	assert.InDelta(t, 150.7922555571461, got.Longitude, 1e-12)
	assert.InDelta(t, 140, got.ElevationM, 1e-9)
	assert.True(t, got.Valid())
}

func TestResolveRejectsUnsetPosition(t *testing.T) {
	t.Parallel()

	// Syntactically valid, but (0,0) is the Gulf of Guinea. In practice it means
	// the operator never set a position, and accepting it would produce
	// confident nonsense instead of an obvious failure.
	_, err := enrichment.StationConfig{Latitude: "0", Longitude: "0"}.Resolve()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unset")
}

func TestResolveRejectsOutOfRange(t *testing.T) {
	t.Parallel()

	_, err := enrichment.StationConfig{Latitude: "91", Longitude: "150"}.Resolve()
	assert.ErrorContains(t, err, "latitude")

	_, err = enrichment.StationConfig{Latitude: "-34", Longitude: "181"}.Resolve()
	assert.ErrorContains(t, err, "longitude")
}

func TestResolveRejectsImplausibleElevation(t *testing.T) {
	t.Parallel()

	// 460 feet entered as metres would be a plausible-looking mistake; 46000
	// would not survive. The bound catches the units error that actually
	// happens, where a wrong elevation quietly biases every lag correction.
	_, err := enrichment.StationConfig{
		Latitude: "-34.11", Longitude: "150.79", ElevationM: 46000,
	}.Resolve()
	assert.ErrorContains(t, err, "implausible")
}

func TestConfiguredGatesEnrichment(t *testing.T) {
	t.Parallel()

	// Enrichment and the auto-labelling collector stay inert until a position
	// exists, and the UI needs to be able to say so rather than failing at the
	// first detection.
	assert.False(t, enrichment.StationConfig{}.Configured())
	assert.False(t, enrichment.StationConfig{Latitude: "-34.11"}.Configured())
	assert.True(t, enrichment.StationConfig{Latitude: "-34.11", Longitude: "150.79"}.Configured())
}
