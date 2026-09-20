package processor

import (
	"testing"

	"github.com/bert386/soundnet-go/internal/conf"
)

func settingsWithThresholds(t *testing.T, models, domains map[string]float64) *conf.Settings {
	t.Helper()
	s := &conf.Settings{}
	s.SoundNet = conf.DefaultSoundNetSettings()
	s.SoundNet.Enabled = true
	s.SoundNet.Thresholds = conf.ThresholdSettings{Models: models, Domains: domains}
	return s
}

// The fallback is the station's real threshold, not zero: a test that leaves it
// at zero would have everything clear the bar and prove nothing.
const fallbackThreshold = 0.7

func TestThresholdOverrideLeavesEverythingAloneByDefault(t *testing.T) {
	t.Parallel()

	s := settingsWithThresholds(t, nil, nil)

	got := soundNetThresholdOverride(s, "aircraft", "Aircraft", "CED", fallbackThreshold)

	if got != fallbackThreshold {
		t.Fatalf("got %v, want the threshold untouched at %v", got, fallbackThreshold)
	}
}

func TestThresholdOverrideAppliesADomainThreshold(t *testing.T) {
	t.Parallel()

	s := settingsWithThresholds(t, nil, map[string]float64{"aircraft": 0.25})

	got := soundNetThresholdOverride(s, "aircraft", "Aircraft", "CED", fallbackThreshold)

	if got != 0.25 {
		t.Fatalf("got %v, want 0.25", got)
	}
}

// Viper lower-cases YAML keys while registry IDs are mixed case. An exact match
// would silently never fire - which is the failure this file exists to fix, so
// it must not be reintroduced by the fix.
func TestThresholdOverrideMatchesAModelIDCaseInsensitively(t *testing.T) {
	t.Parallel()

	s := settingsWithThresholds(t, map[string]float64{"yamnet": 0.2}, nil)

	got := soundNetThresholdOverride(s, "aircraft", "Aircraft", "YAMNet", fallbackThreshold)

	if got != 0.2 {
		t.Fatalf("got %v, want 0.2 from a lower-cased key", got)
	}
}

// Domain is the more specific statement and the one an operator can reason
// about from the detection list.
func TestThresholdOverrideDomainBeatsModel(t *testing.T) {
	t.Parallel()

	s := settingsWithThresholds(t,
		map[string]float64{"ced": 0.45},
		map[string]float64{"aircraft": 0.25},
	)

	got := soundNetThresholdOverride(s, "aircraft", "Aircraft", "CED", fallbackThreshold)

	if got != 0.25 {
		t.Fatalf("got %v, want the domain threshold 0.25 to win", got)
	}
}

// A configured model threshold still applies to that model's other classes.
func TestThresholdOverrideFallsBackToModelForAnUnlistedDomain(t *testing.T) {
	t.Parallel()

	s := settingsWithThresholds(t,
		map[string]float64{"CED": 0.45},
		map[string]float64{"aircraft": 0.25},
	)

	got := soundNetThresholdOverride(s, "vehicle", "Vehicle", "CED", fallbackThreshold)

	if got != 0.45 {
		t.Fatalf("got %v, want the model threshold 0.45", got)
	}
}

// No bird may be affected by either map, whatever is configured. A domain
// threshold that reached BirdNET would quietly change species detection.
func TestThresholdOverrideNeverTouchesASpecies(t *testing.T) {
	t.Parallel()

	s := settingsWithThresholds(t, nil, map[string]float64{"other": 0.1, "bird": 0.1})

	got := soundNetThresholdOverride(s, "Turdus migratorius", "American Robin", "BirdNET_V2.4", fallbackThreshold)

	if got != fallbackThreshold {
		t.Fatalf("got %v, want a species left at %v", got, fallbackThreshold)
	}
}

// The master switch has to gate this like everything else in the fork.
func TestThresholdOverrideIgnoredWhenSoundNetIsOff(t *testing.T) {
	t.Parallel()

	s := settingsWithThresholds(t, nil, map[string]float64{"aircraft": 0.25})
	s.SoundNet.Enabled = false

	got := soundNetThresholdOverride(s, "aircraft", "Aircraft", "CED", fallbackThreshold)

	if got != fallbackThreshold {
		t.Fatalf("got %v, want %v with SoundNet disabled", got, fallbackThreshold)
	}
}
