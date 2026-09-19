package eventclass

import "strings"

// Class is what SoundNet knows about one classifier label.
type Class struct {
	// Label is the classifier's own label, verbatim (AudioSet display_name for
	// YAMNet). Kept exact so it round-trips against the model's label file.
	Label string

	// Domain is the event family this class belongs to.
	Domain Domain

	// DefaultEnabled marks classes recorded out of the box. The rest of the
	// ontology stays addressable but silent until enabled in config.
	DefaultEnabled bool

	// AudioSetIndex is the class index in YAMNet's output vector, or -1 for a
	// class that did not come from AudioSet. Recorded because the index, not the
	// name, is what the model actually emits.
	AudioSetIndex int
}

// audioSetClasses maps the AudioSet classes SoundNet cares about onto domains.
//
// Indices and names are taken verbatim from YAMNet's published class map
// (tensorflow/models research/audioset/yamnet/yamnet_class_map.csv, 521 classes).
// The names must match exactly; they are the join key against the model's label
// file. Classes absent from this table are not unsupported - they resolve to
// DomainOther and stay disabled by default.
var audioSetClasses = []Class{
	// --- Aircraft -----------------------------------------------------------
	// Note that YAMNet already separates jet, propeller and helicopter. The M4
	// sub-classification head is therefore refining a coarse distinction that
	// exists, not inventing one; ADS-B remains the only authority on which
	// specific aircraft it was.
	{"Aircraft", DomainAircraft, true, 329},
	{"Aircraft engine", DomainAircraft, true, 330},
	{"Jet engine", DomainAircraft, true, 331},
	{"Propeller, airscrew", DomainAircraft, true, 332},
	{"Helicopter", DomainAircraft, true, 333},
	{"Fixed-wing aircraft, airplane", DomainAircraft, true, 334},

	// --- Road vehicles ------------------------------------------------------
	// "Car passing by" is the Doppler case specifically: a pass-by, not a
	// stationary engine, which is what makes speed and CPA recoverable.
	{"Vehicle", DomainVehicle, true, 294},
	{"Motor vehicle (road)", DomainVehicle, true, 300},
	{"Car", DomainVehicle, true, 301},
	{"Car passing by", DomainVehicle, true, 308},
	{"Race car, auto racing", DomainVehicle, false, 309},
	{"Truck", DomainVehicle, true, 310},
	{"Air brake", DomainVehicle, false, 311},
	{"Bus", DomainVehicle, false, 315},
	{"Motorcycle", DomainVehicle, true, 320},
	{"Traffic noise, roadway noise", DomainVehicle, false, 321},
	{"Engine", DomainVehicle, false, 337},
	{"Light engine (high frequency)", DomainVehicle, false, 338},
	{"Medium engine (mid frequency)", DomainVehicle, false, 342},
	{"Heavy engine (low frequency)", DomainVehicle, false, 343},
	// Engine knocking is the nearest AudioSet class to a vehicle backfire, which
	// makes it central to the gunshot confusion set below.
	{"Engine knocking", DomainVehicle, true, 344},
	{"Engine starting", DomainVehicle, false, 345},

	// --- Impulses -----------------------------------------------------------
	{"Explosion", DomainImpulse, true, 420},
	{"Gunshot, gunfire", DomainImpulse, true, 421},
	{"Machine gun", DomainImpulse, true, 422},
	{"Fusillade", DomainImpulse, true, 423},
	{"Artillery fire", DomainImpulse, true, 424},
	{"Cap gun", DomainImpulse, false, 425},
	{"Fireworks", DomainImpulse, true, 426},
	{"Firecracker", DomainImpulse, true, 427},
	{"Burst, pop", DomainImpulse, false, 428},
	{"Boom", DomainImpulse, true, 430},

	// --- Weather ------------------------------------------------------------
	{"Thunderstorm", DomainWeather, true, 280},
	{"Thunder", DomainWeather, true, 281},
	{"Rain", DomainWeather, false, 283},
	{"Raindrop", DomainWeather, false, 284},
	{"Rain on surface", DomainWeather, false, 285},
	{"Wind", DomainWeather, false, 277},
	// Wind noise on the microphone is a recording artefact rather than an event.
	// Off by default, but worth keeping addressable: a run of it explains a gap
	// in detections better than silence does.
	{"Wind noise (microphone)", DomainWeather, false, 279},

	// --- Tools --------------------------------------------------------------
	{"Tools", DomainTool, true, 412},
	{"Hammer", DomainTool, true, 413},
	{"Jackhammer", DomainTool, true, 414},
	{"Sawing", DomainTool, true, 415},
	{"Filing (rasp)", DomainTool, false, 416},
	{"Sanding", DomainTool, false, 417},
	{"Power tool", DomainTool, true, 418},
	{"Drill", DomainTool, true, 419},
	{"Chainsaw", DomainTool, true, 341},
	{"Lawn mower", DomainTool, true, 340},

	// --- Alarms -------------------------------------------------------------
	// No public source can say which siren this was, so enrichment returns
	// nothing for the whole domain. Recorded, never identified.
	{"Siren", DomainAlarm, true, 390},
	{"Civil defense siren", DomainAlarm, true, 391},
	{"Emergency vehicle", DomainAlarm, true, 316},
	{"Police car (siren)", DomainAlarm, true, 317},
	{"Ambulance (siren)", DomainAlarm, true, 318},
	{"Fire engine, fire truck (siren)", DomainAlarm, true, 319},
	{"Smoke detector, smoke alarm", DomainAlarm, false, 393},
	{"Fire alarm", DomainAlarm, false, 394},

	// --- Rail and water -----------------------------------------------------
	{"Rail transport", DomainRail, false, 322},
	{"Train", DomainRail, false, 323},
	{"Train horn", DomainRail, false, 325},
	{"Subway, metro, underground", DomainRail, false, 328},
	{"Boat, Water vehicle", DomainWatercraft, false, 295},
	{"Motorboat, speedboat", DomainWatercraft, false, 298},
	{"Ship", DomainWatercraft, false, 299},
}

// byLabel indexes the table for lookup. Built once at init; the table is static.
var byLabel = func() map[string]Class {
	m := make(map[string]Class, len(audioSetClasses))
	for _, c := range audioSetClasses {
		m[strings.ToLower(c.Label)] = c
	}
	return m
}()

// Lookup resolves a classifier label to what SoundNet knows about it.
//
// An unknown label is not an error. The AudioSet ontology has 521 classes and
// this table maps only those the event taxonomy targets; everything else is a
// real class that simply has no special handling, so it resolves to DomainOther
// and stays disabled by default. found reports whether the label was mapped
// explicitly, which lets callers distinguish "deliberately other" from
// "not yet classified".
func Lookup(label string) (class Class, found bool) {
	if c, ok := byLabel[strings.ToLower(strings.TrimSpace(label))]; ok {
		return c, true
	}
	return Class{Label: label, Domain: DomainOther, DefaultEnabled: false, AudioSetIndex: -1}, false
}

// DefaultEnabled returns the classes recorded out of the box.
func DefaultEnabled() []Class {
	out := make([]Class, 0, len(audioSetClasses))
	for _, c := range audioSetClasses {
		if c.DefaultEnabled {
			out = append(out, c)
		}
	}
	return out
}

// InDomain returns every mapped class in a domain.
func InDomain(d Domain) []Class {
	var out []Class
	for _, c := range audioSetClasses {
		if c.Domain == d {
			out = append(out, c)
		}
	}
	return out
}

// ConfusionSet returns classes a given class is genuinely hard to tell apart
// from using a single microphone.
//
// This drives two things: the low-confidence flag the diagnostics engine raises
// instead of asserting an answer, and the paired review view that turns operator
// judgement into a labelled discrimination set.
//
// The gunshot set is the motivating case. A gunshot, a firecracker, a distant
// explosion and a vehicle backfire ("Engine knocking") all present as a loud
// short transient, and the scope is explicit that this must be flagged rather
// than asserted.
func ConfusionSet(label string) []string {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "gunshot, gunfire", "explosion", "firecracker", "fireworks", "engine knocking", "boom":
		return []string{"Gunshot, gunfire", "Explosion", "Firecracker", "Fireworks", "Engine knocking", "Boom"}
	case "jet engine", "fixed-wing aircraft, airplane", "propeller, airscrew", "helicopter", "aircraft engine":
		return []string{"Jet engine", "Fixed-wing aircraft, airplane", "Propeller, airscrew", "Helicopter", "Aircraft engine"}
	case "sawing", "chainsaw", "power tool", "drill":
		return []string{"Sawing", "Chainsaw", "Power tool", "Drill"}
	case "thunder", "thunderstorm":
		return []string{"Thunder", "Thunderstorm", "Explosion"}
	default:
		return nil
	}
}
