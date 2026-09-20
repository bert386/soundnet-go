package eventclass_test

import (
	"encoding/csv"
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bert386/soundnet-go/internal/eventclass"
)

// loadClassMap reads YAMNet's published class map from testdata.
//
// The fixture is committed rather than downloaded: tests must not touch the
// network (TESTING.md), and pinning the file means an upstream ontology change
// shows up as a deliberate fixture update rather than a mysterious CI failure.
func loadClassMap(t *testing.T) (byIndex map[int]string, byName map[string]int) {
	t.Helper()
	f, err := os.Open("testdata/yamnet_class_map.csv")
	require.NoError(t, err, "YAMNet class map fixture missing")
	defer func() { _ = f.Close() }()

	rows, err := csv.NewReader(f).ReadAll()
	require.NoError(t, err)
	require.Greater(t, len(rows), 1, "class map should have a header and data")

	byIndex = make(map[int]string, len(rows))
	byName = make(map[string]int, len(rows))
	for _, r := range rows[1:] { // skip header: index,mid,display_name
		idx, err := strconv.Atoi(r[0])
		require.NoError(t, err)
		byIndex[idx] = r[2]
		byName[r[2]] = idx
	}
	return byIndex, byName
}

// TestMappedClassesMatchYAMNet is the important test in this package.
//
// Labels are a join key: the mapping is matched against the label the model
// actually emits, so a mistyped display name does not fail loudly - it silently
// never matches, and the class quietly goes unrecognised forever. Checking every
// entry against the real class map makes that failure mode impossible.
func TestMappedClassesMatchYAMNet(t *testing.T) {
	t.Parallel()
	byIndex, byName := loadClassMap(t)
	require.Len(t, byIndex, 521, "YAMNet publishes 521 AudioSet classes")

	for _, d := range eventclass.AllDomains() {
		for _, c := range eventclass.InDomain(d) {
			if c.AudioSetIndex < 0 {
				// A classifier's own label rather than an AudioSet class - BirdNET
				// emits "Gun", which AudioSet calls "Gunshot, gunfire". Checking it
				// against the AudioSet map would be checking the wrong thing.
				continue
			}
			gotIdx, nameExists := byName[c.Label]
			assert.True(t, nameExists,
				"label %q is not an AudioSet display_name; it would never match a real detection", c.Label)
			if nameExists {
				assert.Equal(t, gotIdx, c.AudioSetIndex,
					"label %q is class %d in YAMNet but mapped as %d", c.Label, gotIdx, c.AudioSetIndex)
			}
		}
	}
}

func TestLookupKnownClass(t *testing.T) {
	t.Parallel()

	c, found := eventclass.Lookup("Helicopter")
	require.True(t, found)
	assert.Equal(t, eventclass.DomainAircraft, c.Domain)
	assert.Equal(t, 333, c.AudioSetIndex)
	assert.True(t, c.DefaultEnabled)
}

func TestLookupIsCaseAndSpaceInsensitive(t *testing.T) {
	t.Parallel()

	// Label files and config are hand-edited; a stray space or a lowercased
	// entry should still resolve rather than silently fall through to "other".
	c, found := eventclass.Lookup("  jet ENGINE  ")
	require.True(t, found)
	assert.Equal(t, eventclass.DomainAircraft, c.Domain)
}

func TestUnmappedClassIsNotAnError(t *testing.T) {
	t.Parallel()

	// Most of the 521 classes have no special handling. That is a normal state,
	// not a failure: they resolve to "other" and stay off by default.
	c, found := eventclass.Lookup("Bagpipes")
	assert.False(t, found, "found reports explicit mapping, letting callers tell 'other' from 'unclassified'")
	assert.Equal(t, eventclass.DomainOther, c.Domain)
	assert.False(t, c.DefaultEnabled)
	assert.Equal(t, -1, c.AudioSetIndex)
}

func TestDefaultEnabledIsCuratedNotEverything(t *testing.T) {
	t.Parallel()
	enabled := eventclass.DefaultEnabled()

	assert.NotEmpty(t, enabled)
	// The whole point of curating: YAMNet fires constantly on speech and music,
	// which would bury real events. They must not be on by default.
	for _, c := range enabled {
		assert.NotEqual(t, "Speech", c.Label)
		assert.NotEqual(t, "Music", c.Label)
		assert.NotEqual(t, "Silence", c.Label)
	}
	// Every class the scope's taxonomy targets must actually be on by default,
	// or the system ships unable to detect what it was built for.
	for _, want := range []string{
		"Aircraft", "Helicopter", "Jet engine",
		"Car passing by", "Truck", "Motorcycle",
		"Gunshot, gunfire", "Thunder", "Sawing", "Hammer", "Chainsaw",
	} {
		found := false
		for _, c := range enabled {
			if c.Label == want {
				found = true
				break
			}
		}
		assert.True(t, found, "%q is in the scope taxonomy but is not enabled by default", want)
	}
}

func TestGunshotConfusionSetIncludesBackfire(t *testing.T) {
	t.Parallel()

	// The scope calls gunshot-vs-backfire out as genuinely hard from a single
	// microphone. "Engine knocking" is AudioSet's nearest class to a backfire,
	// so it must be in the set the UI offers for paired review - otherwise the
	// confusion the scope names cannot be resolved by an operator at all.
	set := eventclass.ConfusionSet("Gunshot, gunfire")
	assert.Contains(t, set, "Engine knocking")
	assert.Contains(t, set, "Firecracker")
	assert.Contains(t, set, "Explosion")

	// The relation must be symmetric: arriving from either side offers the same
	// comparison, so review is consistent regardless of what the model guessed.
	assert.Contains(t, eventclass.ConfusionSet("Engine knocking"), "Gunshot, gunfire")
}

func TestConfusionSetEmptyForUnambiguousClass(t *testing.T) {
	t.Parallel()
	assert.Nil(t, eventclass.ConfusionSet("Siren"),
		"a class with no genuine confusion should offer no paired review")
}

func TestDomainCapabilities(t *testing.T) {
	t.Parallel()

	// Enrichment exists only where an authoritative source does. Claiming
	// otherwise would invite fabricated identities, which the scope forbids.
	assert.True(t, eventclass.DomainAircraft.Enrichable(), "ADS-B is authoritative for aircraft")
	assert.True(t, eventclass.DomainWeather.Enrichable(), "lightning networks corroborate thunder")
	assert.False(t, eventclass.DomainImpulse.Enrichable(), "no public authority identifies a gunshot")
	assert.False(t, eventclass.DomainAlarm.Enrichable(), "no public authority identifies a siren")
	assert.False(t, eventclass.DomainVehicle.Enrichable(), "no public authority identifies a passing car")

	// Diagnostics should not burn Pi cycles on domains with nothing to measure.
	assert.True(t, eventclass.DomainVehicle.Diagnosable(), "Doppler yields speed and CPA")
	assert.True(t, eventclass.DomainImpulse.Diagnosable(), "onset counting yields shot counts")
	assert.False(t, eventclass.DomainOther.Diagnosable())
}

func TestEveryMappedClassHasAKnownDomain(t *testing.T) {
	t.Parallel()
	known := make(map[eventclass.Domain]bool)
	for _, d := range eventclass.AllDomains() {
		known[d] = true
	}
	byIndex, _ := loadClassMap(t)
	for idx, name := range byIndex {
		c, found := eventclass.Lookup(name)
		assert.True(t, known[c.Domain], "class %d (%q) resolved to unknown domain %q", idx, name, c.Domain)
		if !found {
			assert.Equal(t, eventclass.DomainOther, c.Domain)
		}
	}
}

// The drum kit that was recorded as a vehicle.
//
// Six rows in one evening at `Vehicle` 0.74 to 0.85, because the taxonomy
// carried no music class and the nearest thing won. Scored on the operator's
// own clips the model was never confused: Drum kit 0.63-0.74 and Drum 0.67-0.73
// on the drum clips, against under 0.05 on every vehicle clip.
func TestPercussionIsRecordedSoADrumKitIsNotAVehicle(t *testing.T) {
	t.Parallel()

	for _, label := range []string{"Drum kit", "Drum", "Cymbal"} {
		c, found := eventclass.Lookup(label)
		if !found {
			t.Errorf("%q is not in the taxonomy, so it can never be emitted", label)
			continue
		}
		assert.Equal(t, eventclass.DomainMusic, c.Domain, label)
		assert.True(t, c.DefaultEnabled,
			"%q must be on by default: a class that is not emitted cannot outrank Vehicle", label)
	}

	// Music itself stays off, and the guard on that is right rather than
	// bureaucratic: YAMNet fires on anything tonal, birdsong included.
	c, found := eventclass.Lookup("Music")
	assert.True(t, found, "Music is mapped so it is addressable")
	assert.False(t, c.DefaultEnabled, "a generic Music class would bury the events this station is for")
}

// The one road-vehicle sub-class this station's models reliably name. Car,
// Truck and Bus are not: a bin truck scores Bus 0.42, Truck 0.23, Car 0.14 -
// and Train 0.59.
func TestReversingBeepsIsRecorded(t *testing.T) {
	t.Parallel()

	c, found := eventclass.Lookup("Reversing beeps")
	assert.True(t, found)
	assert.Equal(t, eventclass.DomainVehicle, c.Domain)
	assert.True(t, c.DefaultEnabled)
}

// The rule that prefers a specific class over its parent only reaches a domain
// listed as a candidate, so this is what lets a drum kit displace Vehicle.
func TestVehicleIsAmbiguousWithMusic(t *testing.T) {
	t.Parallel()

	c, found := eventclass.Lookup("Vehicle")
	assert.True(t, found)
	assert.Contains(t, c.CandidateDomains(), eventclass.DomainMusic)
}
