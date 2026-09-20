package eventclass

import "strings"

// Domain ambiguity: labels that do not determine their own domain.
//
// The taxonomy gives every class one domain, which is the most likely reading of
// it. For most classes that is the whole story - "Helicopter" is airborne and
// nothing else. A few labels are not like that, because AudioSet's ontology is a
// hierarchy and SoundNet's domains are flat: "Vehicle" is the parent of road,
// rail, air and water transport, and "Engine" is the parent of the engines that
// power all four. An aircraft genuinely activates both.
//
// This is not a theoretical concern. On the six operator-confirmed aircraft
// clips (doc/soundnet/GROUND_TRUTH.md), "Vehicle" scored 0.59-0.74 on every one
// while "Aircraft" swung between 0.11 and 0.50 - so the superclass was the
// reliable detector and the specific class was the unreliable one. Assigning
// such a detection to DomainVehicle and stopping there sends the case that most
// needs ADS-B down the one path that never asks it.
//
// The resolution is the same rule the whole enrichment layer runs on: acoustics
// say what kind of thing is out there, an authority says which one it was. So an
// ambiguous label is offered to the authorities for every domain it could
// plausibly be, and a confirmed overflight settles it. Nothing is invented - a
// label with no matching authority still resolves to nothing at all.

// ambiguousDomains lists, per raw label, the domains a class could belong to
// besides the one the taxonomy assigns it.
//
// Deliberately short. Only the superclasses belong here: "Car", "Truck",
// "Motorcycle" and "Car passing by" are road-specific, and a high score on one
// of those is genuinely about a car, so asking ADS-B would spend an API credit
// to learn nothing. Keyed on the raw label because that is what survives both
// the classifier and a Resolve of any of the three label forms.
var ambiguousDomains = map[string][]Domain{
	// AudioSet 294. The top of the transport hierarchy; its children include
	// Motor vehicle (road), Rail transport, Aircraft and Boat.
	// Music is in these lists on the evidence, not on the semantics. A drum kit
	// is not a kind of vehicle in any ontology; it is simply what this station's
	// models call one, at 0.85, six times in one evening, because the taxonomy
	// carried no music class for them to reach for. Listing it lets the rule
	// that prefers a specific class over its parent reach the case. The other
	// use of this table is deciding which authority to ask, and no authority is
	// ever asked about music.
	"vehicle": {DomainAircraft, DomainRail, DomainWatercraft, DomainMusic},

	// AudioSet 337, and also one of BirdNET's own seven non-species labels -
	// which is what makes this mapping worth more than its size. It means a
	// BirdNET "Engine" detection reaches ADS-B on a station where YAMNet is not
	// installed at all.
	"engine": {DomainAircraft, DomainRail, DomainWatercraft, DomainMusic},

	// Thunder and Thunderstorm are here on evidence rather than on ontology,
	// which makes them the odd entries in this table and worth explaining.
	//
	// A jet and distant thunder are both low-frequency broadband with a slow
	// envelope, and on this station the model cannot tell them apart at all:
	// across nineteen operator-reviewed Thunder and Thunderstorm detections,
	// **every one was a passing jet**, at confidences up to 0.94. Not a bias -
	// a complete failure of the distinction.
	//
	// That matters more than the usual false positive because these clear the
	// ordinary threshold comfortably, so nothing downstream challenges them.
	// DomainWeather is enrichable in principle, but only a lightning provider
	// would ever answer for it and none is registered, so a jet recorded as
	// Thunderstorm reaches an authority that cannot help and never reaches the
	// one that can.
	//
	// The primary domain stays weather. Thunder is still thunder, and a station
	// that actually hears a storm should record one; ADS-B simply gets the
	// chance to say when it was an aeroplane.
	"thunder":      {DomainAircraft},
	"thunderstorm": {DomainAircraft},
}

// CandidateDomains returns every domain this class could belong to, its own
// first.
//
// The order matters: the first entry is what the detection is recorded as and
// what the UI shows, and the rest are only questions to put to an authority.
func (c Class) CandidateDomains() []Domain {
	extra := ambiguousDomains[RawLabel(c.Label)]
	if len(extra) == 0 {
		return []Domain{c.Domain}
	}
	out := make([]Domain, 0, len(extra)+1)
	out = append(out, c.Domain)
	for _, d := range extra {
		if d != c.Domain {
			out = append(out, d)
		}
	}
	return out
}

// Enrichable reports whether any authority can identify this class, counting the
// domains it is ambiguous between.
//
// Distinct from Domain.Enrichable, which answers the narrower question and stays
// correct: road traffic has no authority, and a class that really is a car is
// still unidentifiable. This says something different - that this particular
// label leaves open the possibility of a domain that does have one.
func (c Class) Enrichable() bool {
	for _, d := range c.CandidateDomains() {
		if d.Enrichable() {
			return true
		}
	}
	return false
}

// IsAmbiguous reports whether the taxonomy's domain for this class is a best
// reading rather than a settled fact. Used to explain, in the API and in logs,
// why a vehicle detection was put to an aircraft authority.
func IsAmbiguous(label string) bool {
	c, _ := Resolve(strings.TrimSpace(label))
	return len(ambiguousDomains[RawLabel(c.Label)]) > 0
}
