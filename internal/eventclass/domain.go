// Package eventclass maps a classifier's raw label onto SoundNet's event
// taxonomy: which domain an event belongs to, whether it is worth recording by
// default, and which other classes it is commonly confused with.
//
// It deliberately does not duplicate the storage layer's Label abstraction.
// Upstream's v2 datastore already generalises labels beyond species - a Label
// carries a LabelType ("species", "noise") and a nullable TaxonomicClass - so
// this package supplies the SoundNet-specific knowledge that sits on top:
// the domain vocabulary and the AudioSet mapping.
//
// All 521 AudioSet classes remain addressable. Only a curated subset is enabled
// by default, because YAMNet fires continuously on classes like Speech, Music
// and Silence, which would bury real events in the detection list. Everything
// else is one config change away.
package eventclass

// Domain is the coarse family an event belongs to. Domains exist so the rest of
// the system can reason about kinds of event without enumerating classes: the
// diagnostics engine asks "is this an impulse?", the enrichment layer asks "is
// this an aircraft?", and neither needs to know about AudioSet.
type Domain string

const (
	// DomainAircraft covers anything airborne. The only domain with an
	// authoritative external identity source (ADS-B).
	DomainAircraft Domain = "aircraft"

	// DomainVehicle covers road traffic. Doppler analysis applies here: a
	// pass-by has a characteristic frequency shift that yields speed and CPA.
	DomainVehicle Domain = "vehicle"

	// DomainImpulse covers short transients - gunshots, explosions, fireworks,
	// hammer blows. These are counted rather than merely detected, and they are
	// where the honest-uncertainty rule bites hardest.
	DomainImpulse Domain = "impulse"

	// DomainWeather covers thunder and precipitation. Thunder has a corroborating
	// external source (lightning networks); rain and wind do not.
	DomainWeather Domain = "weather"

	// DomainTool covers powered and hand tools, distinguished by texture:
	// continuous broadband with a tonal whine, or rhythmic strokes.
	DomainTool Domain = "tool"

	// DomainAlarm covers sirens and alarms. No public authority can identify a
	// specific siren, so enrichment always returns nothing for this domain.
	DomainAlarm Domain = "alarm"

	// DomainRail and DomainWatercraft are recognised but not targeted by v1
	// diagnostics. They are named rather than lumped into DomainOther so that a
	// site near a railway or harbour can enable them coherently.
	DomainRail       Domain = "rail"
	DomainWatercraft Domain = "watercraft"

	// DomainBiological covers birds and other fauna - upstream's original remit,
	// preserved as one domain among many rather than the only one.
	DomainBiological Domain = "biological"

	// DomainOther is the fallback for the rest of the AudioSet ontology.
	DomainOther Domain = "other"
)

// AllDomains lists every domain, for seeding label types and for UI filters.
func AllDomains() []Domain {
	return []Domain{
		DomainAircraft, DomainVehicle, DomainImpulse, DomainWeather,
		DomainTool, DomainAlarm, DomainRail, DomainWatercraft,
		DomainBiological, DomainOther,
	}
}

// Diagnosable reports whether the diagnostics engine has anything useful to say
// about this domain. Used to skip the DSP stage entirely for domains where it
// would burn Raspberry Pi cycles to produce nothing - a Speech detection gains
// nothing from a Doppler fit.
func (d Domain) Diagnosable() bool {
	switch d {
	case DomainAircraft, DomainVehicle, DomainImpulse, DomainWeather, DomainTool, DomainRail:
		return true
	default:
		return false
	}
}

// Enrichable reports whether any authoritative external source can identify
// events in this domain. False for most domains, and that is the point: the
// enrichment layer must return nothing rather than invent an identity.
func (d Domain) Enrichable() bool {
	return d == DomainAircraft || d == DomainWeather
}
