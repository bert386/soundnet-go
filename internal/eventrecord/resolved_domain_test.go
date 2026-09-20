package eventrecord_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bert386/soundnet-go/internal/eventrecord"
)

// putADSB writes one aircraft identification, optionally recording that the
// provider answered a different question from the one the label implied.
func putADSB(t *testing.T, s *eventrecord.Store, detectionID uint, resolvedDomain string) {
	t.Helper()
	attrs := map[string]any{"hex": "7c617e", "callsign": "RSCU208"}
	if resolvedDomain != "" {
		attrs["soundnetClassifiedDomain"] = "vehicle"
		attrs["soundnetResolvedDomain"] = resolvedDomain
	}
	payload, err := json.Marshal(attrs)
	require.NoError(t, err)
	require.NoError(t, s.PutEnrichment(&eventrecord.Enrichment{
		DetectionID: detectionID,
		Provider:    "adsb",
		Source:      "opensky",
		Payload:     payload,
		Confidence:  0.46,
	}))
}

func TestResolvedDomainsReadsOnlyCorrectedDetections(t *testing.T) {
	t.Parallel()
	s := newStore(t)

	putADSB(t, s, 1237, "aircraft") // heard as a vehicle, identified as an aircraft
	putADSB(t, s, 1225, "")         // heard as an aircraft and identified as one

	got, err := s.ResolvedDomains([]uint{1225, 1237, 9999})
	require.NoError(t, err)

	assert.Equal(t, map[uint]string{1237: "aircraft"}, got,
		"only a detection the provider answered under a different domain carries a correction")
}

// A page of bird detections must not pay for a query, and asking about nothing
// must not be an error.
func TestResolvedDomainsWithNoIDs(t *testing.T) {
	t.Parallel()
	s := newStore(t)

	got, err := s.ResolvedDomains(nil)
	require.NoError(t, err)
	assert.Empty(t, got)
}

// One unparseable provider document costs that row its correction and nothing
// else. Failing the batch would take a whole page of detections away from an
// operator to report a field they never asked for.
func TestResolvedDomainsSurvivesAnUnreadablePayload(t *testing.T) {
	t.Parallel()
	s := newStore(t)

	require.NoError(t, s.PutEnrichment(&eventrecord.Enrichment{
		DetectionID: 700,
		Provider:    "adsb",
		Source:      "opensky",
		Payload:     json.RawMessage("not json at all"),
	}))
	putADSB(t, s, 701, "aircraft")

	got, err := s.ResolvedDomains([]uint{700, 701})
	require.NoError(t, err)
	assert.Equal(t, map[uint]string{701: "aircraft"}, got)
}

// The identity is what makes pass-grouping a fact rather than a guess about
// timing: two detections naming the same aircraft are the same source heard
// twice, and when identity contradicts the timing it is identity that is right.
func TestEnrichmentSummariesCarriesTheIdentity(t *testing.T) {
	t.Parallel()
	s := newStore(t)

	putADSB(t, s, 1237, "aircraft") // resolved, and names 7c617e
	putADSB(t, s, 1225, "")         // identified but not a correction

	got, err := s.EnrichmentSummaries([]uint{1225, 1237})
	require.NoError(t, err)

	assert.Equal(t, "7c617e", got[1237].Identity)
	assert.Equal(t, "aircraft", got[1237].ResolvedDomain)

	assert.Equal(t, "7c617e", got[1225].Identity,
		"a detection whose domain needed no correction is still identified, and still groups")
	assert.Empty(t, got[1225].ResolvedDomain)
}
