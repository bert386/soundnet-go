package conf

// SoundNet: the fork's model config aliases.
//
// ValidAudioModels is hand-maintained because this package cannot import
// internal/classifier - and classifier's TestKnownConfigIDs_MatchesConfValidAudioModels
// is the drift guard that makes the two move together. Registering YAMNet in
// the classifier registry therefore obliges a matching entry here, or a config
// naming YAMNet validates in one path and is rejected in the other.
//
// Extending the map from an init() rather than editing the literal keeps this
// fork's footprint in internal/conf/validate.go at zero, the same pattern
// model_yamnet.go uses for ModelRegistry and EmbeddedCatalog.
//
// The aliases must stay identical to ModelRegistry["YAMNet"].ConfigAliases.
func init() {
	ValidAudioModels["yamnet"] = true
	ValidAudioModels["yamnet-v1"] = true

	ValidAudioModels["ced"] = true
	ValidAudioModels["ced-tiny"] = true
	ValidAudioModels["ced_tiny"] = true
}
