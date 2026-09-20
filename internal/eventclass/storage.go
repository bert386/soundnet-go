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
