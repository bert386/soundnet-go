package detections

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bert386/soundnet-go/internal/datastore"
	"github.com/bert386/soundnet-go/internal/eventclass"
)

// SoundNet: tests for the event-domain detection filter.

func TestExpandCategory(t *testing.T) {
	t.Parallel()

	aircraft, ok := expandCategory("aircraft")
	require.True(t, ok)
	assert.Contains(t, aircraft, "jet")
	assert.Contains(t, aircraft, "helicopter")
	assert.NotContains(t, aircraft, "civil", "a siren is not an aircraft")

	// Several domains at once, as a comma-separated list.
	both, ok := expandCategory("aircraft,alarm")
	require.True(t, ok)
	assert.Contains(t, both, "jet")
	assert.Contains(t, both, "civil")

	// The aggregate.
	events, ok := expandCategory(categoryEvents)
	require.True(t, ok)
	assert.Greater(t, len(events), len(aircraft), "'events' must cover more than one domain")

	// Case and whitespace are a user's business, not a reason to fail.
	spaced, ok := expandCategory(" Aircraft , ALARM ")
	require.True(t, ok)
	assert.ElementsMatch(t, both, spaced)
}

// TestExpandCategoryRejectsTheUnknown is the important one. Silently ignoring an
// unrecognised filter returns every detection, and the user cannot tell that
// from a genuinely unfiltered result - so an unknown value must fail, not widen.
func TestExpandCategoryRejectsTheUnknown(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"spaceship", "aircraft,spaceship", "  ", ",", "bird"} {
		names, ok := expandCategory(bad)
		assert.Falsef(t, ok, "%q should be rejected", bad)
		assert.Emptyf(t, names, "%q must not produce a filter", bad)
	}
	// "bird" is deliberately not a category here: birds are matched by label
	// type, not by these event classes, and accepting it would filter to nothing.
}

func TestApplyCategoryFilter(t *testing.T) {
	t.Parallel()

	var f datastore.AdvancedSearchFilters
	require.True(t, applyCategoryFilter(&f, "aircraft"))
	assert.Contains(t, f.Species, "jet")

	// No category is not an error; it simply leaves the filters alone.
	var none datastore.AdvancedSearchFilters
	require.True(t, applyCategoryFilter(&none, ""))
	assert.Empty(t, none.Species)

	// An explicit species filter is more specific and must win, rather than
	// being silently replaced by the broader domain set.
	chosen := datastore.AdvancedSearchFilters{Species: []string{"Turdus merula"}}
	require.True(t, applyCategoryFilter(&chosen, "aircraft"))
	assert.Equal(t, []string{"Turdus merula"}, chosen.Species)

	// A bad category reports failure and changes nothing.
	var bad datastore.AdvancedSearchFilters
	assert.False(t, applyCategoryFilter(&bad, "spaceship"))
	assert.Empty(t, bad.Species)
}

func TestCategoryOptionsComeFromTheTaxonomy(t *testing.T) {
	t.Parallel()
	opts := categoryOptions()
	require.NotEmpty(t, opts.Categories)

	ids := make([]string, 0, len(opts.Categories))
	for _, c := range opts.Categories {
		ids = append(ids, c.ID)
		assert.Positivef(t, c.Labels, "category %q matches no labels and should not be offered", c.ID)
	}
	assert.Equal(t, categoryEvents, ids[0], "the aggregate should lead")
	assert.Contains(t, ids, string(eventclass.DomainAircraft))

	// Every offered category must actually be usable as a filter.
	for _, id := range ids {
		_, ok := expandCategory(id)
		assert.Truef(t, ok, "offered category %q is not accepted by the filter", id)
	}
}

// TestCategoryForcesAdvancedRouting guards the gap that made the filter a no-op
// in production while every unit test passed.
//
// buildAdvancedSearchFilters - where the category is applied - is only reached
// on the advanced path. needsAdvancedRouting decides that, and a parameter
// missing from it is served by the native handlers instead, which return every
// detection. The result is indistinguishable from a filter that matches
// everything, so nothing looks broken.
func TestCategoryForcesAdvancedRouting(t *testing.T) {
	t.Parallel()

	// "all" has no constant; it is the default when queryType is absent.
	for _, queryType := range []string{"all", queryTypeHourly, queryTypeSpecies, queryTypeSearch} {
		withCategory := &detectionQueryParams{QueryType: queryType, Category: "aircraft"}
		assert.Truef(t, withCategory.needsAdvancedRouting(),
			"queryType %q with a category must route to advanced search, or the filter is ignored", queryType)
	}

	// And it must not force the slower path when nothing asked for it.
	plain := &detectionQueryParams{QueryType: "all"}
	assert.False(t, plain.needsAdvancedRouting(),
		"an unfiltered request should keep using the native handler")
}

// TestCacheKeyIncludesCategory guards a subtle failure: two requests differing
// only by category sharing a cached result would serve aircraft rows to someone
// asking for sirens, which looks exactly like a broken filter.
func TestCacheKeyIncludesCategory(t *testing.T) {
	t.Parallel()
	a := &detectionQueryParams{QueryType: "search", Category: "aircraft"}
	b := &detectionQueryParams{QueryType: "search", Category: "alarm"}
	assert.NotEqual(t, a.advancedSearchCacheKey(), b.advancedSearchCacheKey())

	same := &detectionQueryParams{QueryType: "search", Category: "aircraft"}
	assert.Equal(t, a.advancedSearchCacheKey(), same.advancedSearchCacheKey())
}
