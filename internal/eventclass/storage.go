package eventclass

import "strings"

// SoundNet: the bridge between a taxonomy class and the name it is stored under.
//
// Two different forms of the same label exist, and the difference is the whole
// reason this file is separate from the taxonomy table:
//
//	"Jet engine"   the AudioSet display name, what audioset.go holds
//	"jet_engine"   the raw label a classifier emits and nonbird.classes keys on
//	"jet"          what actually lands in the labels table
//
// The third form is upstream's doing. detection.ExtractScientificName splits a
// label on "_" and keeps the first part, treating it as a scientific name - so
// a multi-word event class is truncated on the way into storage. Perch's
// non-bird classes are stored the same way, and nonbird.IsNonBirdName exists to
// cope with exactly this, so it is a convention rather than a bug to fix here.
//
// Filtering detections by domain therefore has to match on the truncated form.
// That is only sound if no truncated name maps to two domains, which is checked
// by a test rather than assumed: today the 39 default-enabled classes produce no
// cross-domain collisions, and the three tokens that are shared ("aircraft",
// "car", "engine") are shared within a single domain, where it does not matter.

// StorageName returns the name a class is stored under in the labels table.
//
//	"Jet engine"                    -> "jet"
//	"Fire engine, fire truck (siren)" -> "fire"
//	"Helicopter"                    -> "helicopter"
func StorageName(displayName string) string {
	raw := RawLabel(displayName)
	if before, _, found := strings.Cut(raw, "_"); found {
		return before
	}
	return raw
}

// RawLabel converts an AudioSet display name to the form a classifier emits and
// the non-bird category table keys on: lower case, comma-separated parts joined
// with "_and_", spaces as underscores.
//
//	"Jet engine"                 -> "jet_engine"
//	"Child speech, kid speaking" -> "child_speech_and_kid_speaking"
func RawLabel(displayName string) string {
	parts := strings.Split(displayName, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return strings.ToLower(strings.ReplaceAll(strings.Join(parts, "_and_"), " ", "_"))
}

// byRawIndex and byStorageIndex index the taxonomy by the other two label
// forms. Built eagerly in init rather than on first use: Resolve is called from
// the detection pipeline, and lazily filling a package-level map from there is
// a data race the race detector rightly refuses. The maps are a few hundred
// entries, so there is nothing to defer.
var (
	byRawIndex     map[string]Class
	byStorageIndex map[string]Class
)

func init() {
	byRawIndex = make(map[string]Class, len(audioSetClasses)+len(birdNETClasses))
	byStorageIndex = make(map[string]Class, len(audioSetClasses)+len(birdNETClasses))
	for _, c := range allClasses() {
		byRawIndex[RawLabel(c.Label)] = c
		// Keep the first class for a truncated name so the result is stable
		// rather than dependent on table order changing under us.
		if name := StorageName(c.Label); name != "" {
			if _, dup := byStorageIndex[name]; !dup {
				byStorageIndex[name] = c
			}
		}
	}
}

// LookupRaw resolves a raw classifier label ("jet_engine") to its class.
//
// Distinct from Lookup, which keys on the AudioSet display name. Both forms are
// in circulation - the display name in this table, the raw form in the datastore
// and in nonbird.classes - and a lookup that silently accepts only one of them
// would fail exactly where it is most needed.
func LookupRaw(rawLabel string) (Class, bool) {
	c, ok := byRawIndex[strings.ToLower(strings.TrimSpace(rawLabel))]
	return c, ok
}

// DisplayName returns the human-readable name for a raw classifier label.
//
// The datastore splits a label at the first underscore and stores the halves as
// a scientific and a common name, so "jet_engine" reaches the UI as "jet" and
// "engine" - and "pigeon_and_dove" as the frankly baffling "and_dove". Rejoining
// the halves recovers the raw label, and the taxonomy holds the name a person
// should actually see.
//
// ok is false for anything not in the taxonomy, including every bird species,
// so callers leave those untouched.
func DisplayName(scientificName, commonName string) (name string, ok bool) {
	raw := scientificName
	if commonName != "" && !strings.EqualFold(commonName, scientificName) {
		raw = scientificName + "_" + commonName
	}
	c, found := LookupRaw(raw)
	if !found {
		return "", false
	}
	return c.Label, true
}

// Resolve finds a class from a label in whichever form the caller happens to
// hold, trying the display name, the raw label, and the truncated stored name
// in that order.
//
// This exists because three forms of the same label are in circulation and a
// lookup that accepts only one of them fails silently - the class resolves to
// DomainOther, which is diagnosable by nothing and enrichable by nothing, so
// the whole SoundNet pipeline is skipped and the detection simply looks
// uninteresting. That is exactly what happened to two confirmed propeller
// aircraft: stored as "propeller", looked up against "propeller, airscrew",
// matched nothing, and never reached ADS-B.
//
// The truncated form is ambiguous by construction ("aircraft" is both Aircraft
// and Aircraft engine), which is tolerable because a test proves no truncated
// name spans two domains - so the domain, which is what callers act on, is
// always right even when the exact class is a coin toss between siblings.
func Resolve(label string) (class Class, found bool) {
	if c, ok := Lookup(label); ok {
		return c, true
	}
	if c, ok := LookupRaw(label); ok {
		return c, true
	}
	if c, ok := byStorageIndex[strings.ToLower(strings.TrimSpace(label))]; ok {
		return c, true
	}
	return Class{Label: label, Domain: DomainOther, DefaultEnabled: false, AudioSetIndex: -1}, false
}

// StorageNames returns the stored names of a domain's default-enabled classes,
// deduplicated and in a stable order, for use as a detection filter.
//
// Only default-enabled classes: a class the taxonomy maps but leaves disabled is
// never emitted, so including it would widen the filter to names that cannot
// appear.
func StorageNames(d Domain) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, 8)
	for _, c := range InDomain(d) {
		if !c.DefaultEnabled {
			continue
		}
		name := StorageName(c.Label)
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

// FilterableDomains lists the domains worth offering as a detection filter.
//
// DomainOther is excluded: it is the fallback for the rest of the AudioSet
// ontology and has no default-enabled classes, so filtering by it would always
// return nothing. DomainBiological is excluded for the opposite reason - birds
// are the overwhelming majority of detections and are not identified by these
// event classes at all; filtering to them needs the label type, not this table.
func FilterableDomains() []Domain {
	out := make([]Domain, 0, len(AllDomains()))
	for _, d := range AllDomains() {
		if d == DomainOther || d == DomainBiological {
			continue
		}
		if len(StorageNames(d)) == 0 {
			continue
		}
		out = append(out, d)
	}
	return out
}

// StorageNamesForDomains returns the union of StorageNames across domains, for a
// filter naming more than one.
func StorageNamesForDomains(domains []Domain) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, 16)
	for _, d := range domains {
		for _, name := range StorageNames(d) {
			if _, dup := seen[name]; dup {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, name)
		}
	}
	return out
}

// ParseDomain resolves a domain name from an API query parameter.
func ParseDomain(s string) (Domain, bool) {
	want := Domain(strings.ToLower(strings.TrimSpace(s)))
	for _, d := range AllDomains() {
		if d == want {
			return d, true
		}
	}
	return "", false
}
