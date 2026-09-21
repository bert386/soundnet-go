package nonbird

import (
	"maps"
	"strings"
)

// SoundNet: the AudioSet classes YAMNet can emit that upstream's table does not
// already cover.
//
// This is not cosmetic. A label absent from `classes` takes the species branch
// in the datastore (see labelTypeForRawLabel), so it is stored with the Aves
// taxonomic class - an aircraft filed as a bird. Of the 66 classes SoundNet's
// event taxonomy maps, 33 were missing here, 17 of them enabled by default.
//
// Categories follow the nearest AudioSet ontology parent that upstream already
// categorised, rather than being judged case by case:
//
//	Explosion      -> mechanical  (gunshot_and_gunfire, fireworks already are)
//	Alarm          -> mechanical  ("alarm" already is)
//	Engine/Vehicle -> mechanical  ("engine", "vehicle", "truck" already are)
//	Tools          -> mechanical  ("power_tool", "sawing", "drill" already are)
//	Wind/Rain      -> environment ("wind", "rain" already are)
//
// Keys are the normalised raw-label form the models emit: lower case, each
// comma-separated part joined with "_and_", spaces as underscores. They are
// derived from the AudioSet display names mechanically, not typed by hand, and
// a test checks all 66 against the committed class map.
var soundNetClasses = map[string]Category{
	// --- Aircraft ---------------------------------------------------------
	// "aircraft" itself is already upstream; these are its ontology children.
	"aircraft_engine":        CategoryMechanical,
	"jet_engine":             CategoryMechanical,
	"propeller_and_airscrew": CategoryMechanical,
	"helicopter":             CategoryMechanical,

	// --- Sirens and alarms ------------------------------------------------
	// Children of "siren" and "alarm", both already mechanical upstream. The
	// smoke and fire alarms are household electronics and a case could be made
	// for CategoryDevice, but following the ontology parent keeps the rule
	// mechanical rather than leaving it to taste.
	"civil_defense_siren":                CategoryMechanical,
	"emergency_vehicle":                  CategoryMechanical,
	"police_car_(siren)":                 CategoryMechanical,
	"ambulance_(siren)":                  CategoryMechanical,
	"fire_engine_and_fire_truck_(siren)": CategoryMechanical,
	"smoke_detector_and_smoke_alarm":     CategoryMechanical,
	"fire_alarm":                         CategoryMechanical,

	// --- Impulsive ---------------------------------------------------------
	// AudioSet files all of these under Explosion, whose other children
	// (gunshot_and_gunfire, fireworks, explosion) are already mechanical.
	"machine_gun":    CategoryMechanical,
	"fusillade":      CategoryMechanical,
	"artillery_fire": CategoryMechanical,
	"firecracker":    CategoryMechanical,
	"cap_gun":        CategoryMechanical,
	"burst_and_pop":  CategoryMechanical,

	// --- Tools -------------------------------------------------------------
	"jackhammer":    CategoryMechanical,
	"chainsaw":      CategoryMechanical,
	"lawn_mower":    CategoryMechanical,
	"filing_(rasp)": CategoryMechanical,
	"sanding":       CategoryMechanical,

	// --- Road vehicles -----------------------------------------------------
	// The three engine size classes are what make a truck separable from a
	// motorcycle before any sub-classification head exists.
	"engine_knocking":               CategoryMechanical,
	"air_brake":                     CategoryMechanical,
	"light_engine_(high_frequency)": CategoryMechanical,
	"medium_engine_(mid_frequency)": CategoryMechanical,
	"heavy_engine_(low_frequency)":  CategoryMechanical,

	// --- Rail and water ----------------------------------------------------
	"train_horn":              CategoryMechanical,
	"motorboat_and_speedboat": CategoryMechanical,
	"ship":                    CategoryMechanical,

	// --- Weather -----------------------------------------------------------
	// "wind noise (microphone)" is the microphone artefact class, not weather
	// as such, but it is environmental in origin and never a species.
	"rain_on_surface":         CategoryEnvironment,
	"wind_noise_(microphone)": CategoryEnvironment,

	// --- Added with the taxonomy, missed here --------------------------------
	// Both went into internal/eventclass on 2026-09-20 without an entry in this
	// table, so every detection of either was stored with the Aves taxonomic
	// class - the "aircraft filed as a bird" failure this table exists to
	// prevent. The coverage test caught it; nobody ran it until 2026-09-21.
	"reversing_beeps": CategoryMechanical, // a vehicle's, like "truck" and "engine"
	"caterwaul":       CategoryAnimal,     // a cat's, like "meow" and "purr"

	// --- Unstructured ------------------------------------------------------
	"noise": CategoryNoise,
}

// init folds the SoundNet classes into the upstream table.
//
// `classes` is a package-level map, so extending it from here keeps this fork's
// footprint in classes.go at zero - the same pattern model_yamnet.go uses for
// ModelRegistry and EmbeddedCatalog.
//
// firstTokenSet is rebuilt afterwards because it is derived from `classes`, and
// Go gives no guaranteed ordering between this init and the one in nonbird.go.
// Rebuilding is idempotent and cheap, so it is correct whichever runs first:
// if this one runs first, nonbird.go rebuilds over the top with the same
// result; if it runs second, the rebuild here picks up the new keys. Without
// it, IsNonBirdName would miss every multi-word class added above, and would
// miss it silently.
func init() {
	maps.Copy(classes, soundNetClasses)
	rebuildFirstTokenSet()
}

// rebuildFirstTokenSet re-derives firstTokenSet from the current `classes`.
//
// Deliberately a copy of the loop in nonbird.go's init rather than a shared
// helper extracted from it: five duplicated lines here cost less than an edit
// to an upstream file, and the two cannot drift meaningfully because both are
// just "first token of every multi-word key".
func rebuildFirstTokenSet() {
	rebuilt := make(map[string]struct{}, len(classes))
	for k := range classes {
		if before, _, found := strings.Cut(k, "_"); found {
			rebuilt[before] = struct{}{}
		}
	}
	firstTokenSet = rebuilt
}
