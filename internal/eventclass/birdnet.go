package eventclass

// BirdNET's own non-species labels.
//
// BirdNET v2.4 classifies 6522 labels, of which seven are not species: Dog,
// Engine, Environmental, Fireworks, Gun, Noise and Siren. Mapping them matters
// out of proportion to their number, because it means the diagnostics layer
// works with the model the system already ships - a gunshot gets its onsets
// counted, and a passing engine gets a Doppler fit, before YAMNet is installed
// at all.
//
// Only two of the seven appear here. The other five - Dog, Engine, Fireworks,
// Noise and Siren - are spelled exactly as AudioSet spells them, so they already
// resolve through the AudioSet table and are mapped there with their real class
// indices. Duplicating them here would shadow those entries and throw the
// indices away, which is precisely the bug an earlier version of this file had.
var birdNETClasses = []Class{
	// Gun is the motivating case: onset counting turns "a gunshot" into "three
	// shots", which is the difference between a detection and a report.
	// AudioSet calls the same thing "Gunshot, gunfire".
	{"Gun", DomainImpulse, true, -1},

	// Environmental is BirdNET saying it heard something it could not place.
	// Mapped so it resolves to a known domain rather than falling through as
	// unclassified, but disabled: recording it would fill the list with
	// non-events.
	{"Environmental", DomainOther, false, -1},
}
