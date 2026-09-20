package processor

// SOUNDNET: thresholds that mean the same thing across models.
//
// Every model but Bat, Perch and BirdNET v3 inherits `birdnet.threshold`, which
// on this station is 0.7. That number was chosen for BirdNET's species head and
// does not transfer. YAMNet emits a per-class sigmoid quantised to 1/256, and on
// the operator's labelled clips its aircraft classes top out around 0.2; CED
// reaches 0.5 on the same audio. A single number therefore sets three different
// effective sensitivities, and the two event models are all but silent for every
// class that corroboration does not rescue.
//
// So a threshold can be set per model, or - more usefully - per event domain,
// because "how confident must the station be before it records an aircraft" is a
// question about aircraft rather than about whichever model happened to hear it.
//
// Both default to empty, which leaves every threshold exactly as it was.

import (
	"strings"

	"github.com/bert386/soundnet-go/internal/conf"
	"github.com/bert386/soundnet-go/internal/eventclass"
)

// soundNetThresholdOverride returns the configured threshold for this detection,
// or fallback when nothing applies.
//
// Domain beats model: it is the more specific statement, and it is the one an
// operator can reason about from the detection list. Neither is consulted for a
// label outside the event taxonomy, so no bird is affected by either.
func soundNetThresholdOverride(settings *conf.Settings, scientificName, commonName, modelID string, fallback float32) float32 {
	if settings == nil || !settings.SoundNet.Enabled {
		return fallback
	}
	cfg := settings.SoundNet.Thresholds
	if len(cfg.Domains) == 0 && len(cfg.Models) == 0 {
		return fallback
	}

	if len(cfg.Domains) > 0 {
		if class, found := eventclass.Resolve(scientificName); found {
			if v, ok := lookupFold(cfg.Domains, string(class.Domain)); ok {
				return float32(v)
			}
		} else if class, found := eventclass.Resolve(commonName); found {
			// The stored scientific name is a truncated form and does not always
			// resolve; the display name is the other form the taxonomy indexes.
			if v, ok := lookupFold(cfg.Domains, string(class.Domain)); ok {
				return float32(v)
			}
		}
	}

	if v, ok := lookupFold(cfg.Models, modelID); ok {
		return float32(v)
	}
	return fallback
}

// lookupFold reads a configured map case-insensitively.
//
// Viper lower-cases the keys it reads from YAML while model registry IDs are
// mixed case ("BirdNET_V2.4", "YAMNet"), so an exact match would silently never
// fire - the failure mode this whole file exists to fix.
func lookupFold(m map[string]float64, key string) (float64, bool) {
	if len(m) == 0 || key == "" {
		return 0, false
	}
	if v, ok := m[key]; ok {
		return v, true
	}
	folded := strings.ToLower(strings.TrimSpace(key))
	for k, v := range m {
		if strings.ToLower(strings.TrimSpace(k)) == folded {
			return v, true
		}
	}
	return 0, false
}
