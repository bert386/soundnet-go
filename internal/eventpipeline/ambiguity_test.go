package eventpipeline_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bert386/soundnet-go/internal/enrichment"
	"github.com/bert386/soundnet-go/internal/eventclass"
	"github.com/bert386/soundnet-go/internal/eventpipeline"
)

// domainResolver answers for named domains only and records the order in which
// it was asked, which is the thing these tests are actually about: a label that
// does not determine its own domain must reach the authorities for the domains
// it could be, and must not reach them for the domains it could not.
type domainResolver struct {
	answers map[string]*enrichment.Identity
	asked   []string
}

func (r *domainResolver) Resolve(_ context.Context, req *enrichment.Request) (*enrichment.Identity, error) {
	r.asked = append(r.asked, req.Domain)
	if id, ok := r.answers[req.Domain]; ok {
		return id, nil
	}
	return nil, enrichment.ErrNoMatch
}

func (r *domainResolver) HasProviderFor(d string) bool {
	_, ok := r.answers[d]
	return ok
}

func enrichingAnalyser(res eventpipeline.Resolver) *eventpipeline.Analyser {
	cfg := eventpipeline.DefaultConfig()
	cfg.EnrichmentEnabled = true
	cfg.Station = station
	return &eventpipeline.Analyser{Config: cfg, Store: newStore(), Resolver: res}
}

// TestVehicleReachesTheAircraftAuthority is the case the operator's ground truth
// produced. "Vehicle" scored 0.59-0.74 on every confirmed aircraft clip while
// "Aircraft" swung between 0.11 and 0.50, so the detection that most needs ADS-B
// is frequently the one recorded as a vehicle - and DomainVehicle is not
// enrichable, so before this it was the one path that never asked.
func TestVehicleReachesTheAircraftAuthority(t *testing.T) {
	t.Parallel()
	res := &domainResolver{answers: map[string]*enrichment.Identity{
		"aircraft": {Provider: "adsb", Source: "opensky", Confidence: 0.9,
			Attributes: map[string]any{"hex": "7c7801", "registration": "VH-XZN"}},
	}}
	a := enrichingAnalyser(res)

	got, err := a.Process(t.Context(), baseInput("Vehicle"))
	require.NoError(t, err)

	assert.True(t, got.IdentityResolved, "an overflight was overhead and ADS-B knew it")
	assert.Equal(t, eventclass.DomainVehicle, got.Domain, "the acoustic reading is unchanged")
	assert.Equal(t, eventclass.DomainAircraft, got.ResolvedDomain, "the authority settled it")
	assert.Equal(t, []string{"vehicle", "aircraft"}, res.asked,
		"its own domain first, then the ones it could also be; stopping at the first answer")
}

// TestAmbiguityIsRecordedOnTheStoredRow guards the part that matters months
// later: a detection recorded as a vehicle carrying an aircraft's registration
// looks like a bug unless the row says which question was asked.
func TestAmbiguityIsRecordedOnTheStoredRow(t *testing.T) {
	t.Parallel()
	store := newStore()
	res := &domainResolver{answers: map[string]*enrichment.Identity{
		"aircraft": {Provider: "adsb", Source: "opensky",
			Attributes: map[string]any{"registration": "VH-XZN"}},
	}}
	cfg := eventpipeline.DefaultConfig()
	cfg.EnrichmentEnabled = true
	cfg.Station = station
	a := &eventpipeline.Analyser{Config: cfg, Store: store, Resolver: res}

	_, err := a.Process(t.Context(), baseInput("Vehicle"))
	require.NoError(t, err)
	require.Len(t, store.enrichments, 1)

	payload := string(store.enrichments[0].Payload)
	assert.Contains(t, payload, "VH-XZN", "the provider's own attributes are not disturbed")
	assert.Contains(t, payload, "soundnetClassifiedDomain")
	assert.Contains(t, payload, "soundnetResolvedDomain")
}

// TestRoadSpecificLabelsDoNotSpendCredits is the other half of the contract. The
// whole reason the ambiguity table is short is that "Car" really is a car, and
// asking an aircraft authority about it would spend an API credit to learn
// nothing - on a class that fires far more often than the superclass does.
func TestRoadSpecificLabelsDoNotSpendCredits(t *testing.T) {
	t.Parallel()
	for _, label := range []string{"Car", "Car passing by", "Truck", "Motorcycle"} {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			res := &domainResolver{}
			a := enrichingAnalyser(res)

			_, err := a.Process(t.Context(), baseInput(label))
			require.NoError(t, err)
			assert.NotContains(t, res.asked, "aircraft",
				"a road-specific label has no aircraft reading to check")
		})
	}
}

// TestUnambiguousClassesAskOnce pins that this costs nothing for the classes it
// was not written for: an aircraft label still makes exactly one query.
func TestUnambiguousClassesAskOnce(t *testing.T) {
	t.Parallel()
	res := &domainResolver{}
	a := enrichingAnalyser(res)

	_, err := a.Process(t.Context(), baseInput("Aircraft"))
	require.NoError(t, err)
	assert.Equal(t, []string{"aircraft"}, res.asked)
}

// TestOwnDomainWinsWhenBothCouldAnswer pins the precedence. The acoustic reading
// is the first candidate, so an authority for the class's own domain settles it
// without a second question being asked.
func TestOwnDomainWinsWhenBothCouldAnswer(t *testing.T) {
	t.Parallel()
	res := &domainResolver{answers: map[string]*enrichment.Identity{
		"vehicle":  {Provider: "hypothetical", Source: "test"},
		"aircraft": {Provider: "adsb", Source: "opensky"},
	}}
	a := enrichingAnalyser(res)

	got, err := a.Process(t.Context(), baseInput("Vehicle"))
	require.NoError(t, err)
	assert.Equal(t, eventclass.DomainVehicle, got.ResolvedDomain)
	assert.Equal(t, []string{"vehicle"}, res.asked)
}

// TestBirdNETEngineReachesADSB is worth its own test because it is the part that
// works without YAMNet at all. "Engine" is one of BirdNET v2.4's seven
// non-species labels, so a station running only the shipped model can identify
// an overflight.
func TestBirdNETEngineReachesADSB(t *testing.T) {
	t.Parallel()
	res := &domainResolver{answers: map[string]*enrichment.Identity{
		"aircraft": {Provider: "adsb", Source: "opensky"},
	}}
	a := enrichingAnalyser(res)

	got, err := a.Process(t.Context(), baseInput("Engine"))
	require.NoError(t, err)
	assert.True(t, got.IdentityResolved)
	assert.Equal(t, eventclass.DomainAircraft, got.ResolvedDomain)
}
