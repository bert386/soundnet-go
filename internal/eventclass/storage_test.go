package eventclass_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bert386/soundnet-go/internal/eventclass"
)

func TestRawLabelAndStorageName(t *testing.T) {
	t.Parallel()
	for display, want := range map[string][2]string{
		// display: {raw label, stored name}
		"Jet engine":                      {"jet_engine", "jet"},
		"Helicopter":                      {"helicopter", "helicopter"},
		"Fire engine, fire truck (siren)": {"fire_engine_and_fire_truck_(siren)", "fire"},
		"Child speech, kid speaking":      {"child_speech_and_kid_speaking", "child"},
		"Propeller, airscrew":             {"propeller_and_airscrew", "propeller"},
	} {
		assert.Equal(t, want[0], eventclass.RawLabel(display), "raw label for %q", display)
		assert.Equal(t, want[1], eventclass.StorageName(display), "stored name for %q", display)
	}
}

// TestStoredNamesDoNotCollideAcrossDomains is the assumption the whole domain
// filter rests on.
//
// The datastore truncates a label at the first underscore, so "Jet engine" is
// stored as "jet". Filtering detections by domain matches on that truncated
// name, which is only sound while no truncated name belongs to two domains. If
// a future taxonomy addition breaks that, a domain filter would silently return
// detections from another domain - so this fails loudly instead.
func TestStoredNamesDoNotCollideAcrossDomains(t *testing.T) {
	t.Parallel()

	owner := map[string]eventclass.Domain{}
	for _, d := range eventclass.AllDomains() {
		for _, name := range eventclass.StorageNames(d) {
			if prev, seen := owner[name]; seen && prev != d {
				t.Errorf("stored name %q maps to both %q and %q; domain filtering would mix them",
					name, prev, d)
				continue
			}
			owner[name] = d
		}
	}
	assert.NotEmpty(t, owner, "some classes must be filterable")
}

func TestStorageNamesCoverTheDomainsWeCareAbout(t *testing.T) {
	t.Parallel()

	aircraft := eventclass.StorageNames(eventclass.DomainAircraft)
	assert.Contains(t, aircraft, "jet")
	assert.Contains(t, aircraft, "helicopter")
	// Deduplicated: "Aircraft" and "Aircraft engine" both store as "aircraft".
	assert.Len(t, aircraft, len(uniq(aircraft)), "names must be deduplicated")

	// Only default-enabled classes: a disabled class is never emitted, so
	// including it would widen the filter to names that cannot appear.
	for _, d := range eventclass.AllDomains() {
		enabled := map[string]bool{}
		for _, c := range eventclass.InDomain(d) {
			if c.DefaultEnabled {
				enabled[eventclass.StorageName(c.Label)] = true
			}
		}
		for _, name := range eventclass.StorageNames(d) {
			assert.Truef(t, enabled[name], "%q in domain %q is not default-enabled", name, d)
		}
	}
}

func TestFilterableDomainsExcludeTheUselessOnes(t *testing.T) {
	t.Parallel()
	got := eventclass.FilterableDomains()
	require.NotEmpty(t, got)

	for _, d := range got {
		assert.NotEqual(t, eventclass.DomainOther, d, "Other has no enabled classes; filtering by it returns nothing")
		assert.NotEqual(t, eventclass.DomainBiological, d, "birds are filtered by label type, not by these event classes")
		assert.NotEmptyf(t, eventclass.StorageNames(d), "domain %q is offered but matches nothing", d)
	}
	assert.Contains(t, got, eventclass.DomainAircraft)
}

func TestParseDomain(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"aircraft", "Aircraft", " AIRCRAFT "} {
		d, ok := eventclass.ParseDomain(in)
		assert.True(t, ok, "should parse %q", in)
		assert.Equal(t, eventclass.DomainAircraft, d)
	}
	_, ok := eventclass.ParseDomain("spaceship")
	assert.False(t, ok, "an unknown domain must be rejected, not silently ignored")
}

func TestStorageNamesForDomainsIsAUnion(t *testing.T) {
	t.Parallel()
	both := eventclass.StorageNamesForDomains([]eventclass.Domain{
		eventclass.DomainAircraft, eventclass.DomainAlarm,
	})
	assert.Contains(t, both, "jet")
	assert.Contains(t, both, "civil")
	assert.Len(t, both, len(uniq(both)), "the union must be deduplicated")
}

func uniq(in []string) []string {
	seen := map[string]struct{}{}
	out := in[:0:0]
	for _, s := range in {
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
