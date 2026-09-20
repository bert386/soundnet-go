package conf

import "github.com/spf13/viper"

// SoundNet configuration.
//
// Kept in its own file so the fork's settings are one added file plus a single
// field on Settings, rather than edits scattered through upstream's config.
//
// Note what is NOT here: latitude and longitude. Upstream already has them, as
// BirdNET.Latitude and BirdNET.Longitude, already editable in the settings UI
// and already used for range filtering. Adding a second pair would create two
// places to set one fact, and they would drift. Only elevation is added, because
// upstream has no use for it and therefore never collected it.

// SoundNetSettings configures the SoundNet layers.
type SoundNetSettings struct {
	// Enabled is the master switch. Off by default: everything below either
	// costs Raspberry Pi cycles or makes outbound calls.
	Enabled bool `yaml:"enabled" json:"enabled" mapstructure:"enabled"`

	Station     StationSettings     `yaml:"station" json:"station" mapstructure:"station"`
	Diagnostics DiagnosticsSettings `yaml:"diagnostics" json:"diagnostics" mapstructure:"diagnostics"`
	Enrichment  EnrichmentSettings  `yaml:"enrichment" json:"enrichment" mapstructure:"enrichment"`
}

// StationSettings holds what upstream's location settings do not.
type StationSettings struct {
	// ElevationM is metres above sea level.
	//
	// This is not decoration. Acoustic-lag correction works from slant range -
	// the straight-line distance sound actually travelled - which combines
	// horizontal distance with the height difference between aircraft and
	// microphone. An error here biases every correction: roughly 0.7 s per 250 m
	// at typical overflight altitudes, which is enough to match the wrong
	// aircraft in busy airspace.
	//
	// Manual entry, because browser geolocation altitude is unreliable.
	ElevationM float64 `yaml:"elevationm" json:"elevationM" mapstructure:"elevationm"`
}

// DiagnosticsSettings controls the DSP properties layer.
type DiagnosticsSettings struct {
	// Enabled gates the whole layer. Measured at 24.8 ms per clip on a
	// Raspberry Pi 4 against a 100 ms budget, so the cost is modest - but it is
	// still cost, and the scope requires it default off.
	Enabled bool `yaml:"enabled" json:"enabled" mapstructure:"enabled"`

	// MaxClipMs caps how much audio a single analysis will process, so a long
	// retained clip cannot blow the per-detection budget.
	MaxClipMs int `yaml:"maxclipms" json:"maxClipMs" mapstructure:"maxclipms"`

	// CalibratedMic declares a microphone with known sensitivity. Only then is
	// an absolute distance in metres meaningful; without it the near/far
	// indicator stays relative, which is the honest reading.
	CalibratedMic bool `yaml:"calibratedmic" json:"calibratedMic" mapstructure:"calibratedmic"`

	// ReferenceSPLAt1m is the expected source level one metre away, in dB.
	// Used only when CalibratedMic is set.
	ReferenceSPLAt1m float64 `yaml:"referencesplat1m" json:"referenceSplAt1m" mapstructure:"referencesplat1m"`
}

// EnrichmentSettings controls external identity resolution.
type EnrichmentSettings struct {
	// Enabled gates all outbound identification. Off by default: this is the
	// only part of SoundNet that contacts anything outside the machine, and
	// upstream's privacy stance is local-only unless explicitly opted in.
	Enabled bool `yaml:"enabled" json:"enabled" mapstructure:"enabled"`

	// CorroborationThreshold lowers the acoustic bar for classes an authority
	// can confirm, and zero - the default - leaves the behaviour unchanged.
	//
	// It exists to break an ordering problem. A distant aircraft scores
	// 0.10-0.15, a sensible threshold is 0.7, so no detection is created; and
	// because none is created, nothing reaches enrichment to discover that an
	// aircraft really was overhead. The evidence that would justify keeping the
	// detection sits behind the threshold that discards it.
	//
	// Set above zero and a detection in an enrichable domain scoring at least
	// this much is held rather than dropped, and kept only if an authority
	// independently places a credible source overhead. It is not a lower
	// threshold: nothing is admitted on the acoustic score alone, so the
	// detection list does not fill with quiet guesses.
	//
	// It costs API credits - one query per candidate that would otherwise have
	// been dropped in silence - which is why it is off by default and why the
	// ADS-B client reuses a fetched sky for five seconds.
	CorroborationThreshold float64 `yaml:"corroborationthreshold" json:"corroborationThreshold" mapstructure:"corroborationthreshold"`

	ADSB ADSBSettings `yaml:"adsb" json:"adsb" mapstructure:"adsb"`
}

// ADSBSettings configures aircraft identification.
type ADSBSettings struct {
	Enabled bool `yaml:"enabled" json:"enabled" mapstructure:"enabled"`

	// CredentialsPath points at a file holding the OpenSky client credentials.
	//
	// A path, never the secret itself. Credentials in a versioned config file
	// end up in backups, in support dumps and in screenshots; a path keeps them
	// in exactly one place the operator controls.
	//
	// Authentication is mandatory rather than preferred: anonymous OpenSky
	// access ignores the time parameter and only ever returns the current sky,
	// which makes the acoustic-lag correction - the entire point - impossible.
	CredentialsPath string `yaml:"credentialspath" json:"credentialsPath" mapstructure:"credentialspath"`

	// SearchRadiusM bounds the query box around the station. Kept small on
	// purpose: OpenSky charges by bounding-box area, and anything up to 25
	// square degrees costs a single credit.
	SearchRadiusM float64 `yaml:"searchradiusm" json:"searchRadiusM" mapstructure:"searchradiusm"`

	// MaxRangeM is the slant range beyond which a match is not credible. Site
	// dependent: a quiet rural station hears aircraft much further off than an
	// urban one.
	MaxRangeM float64 `yaml:"maxrangem" json:"maxRangeM" mapstructure:"maxrangem"`

	// CreditFloor is the API credit reserve held back for runtime enrichment.
	// The daily allowance is shared with the auto-labelling collector, and
	// without a floor a busy collector would exhaust it and leave real
	// detections unidentifiable for the rest of the day.
	CreditFloor int `yaml:"creditfloor" json:"creditFloor" mapstructure:"creditfloor"`

	// ResolveAircraftDetail turns hex codes and callsigns into registration,
	// type, operator and flight route via a third-party lookup. Separate from
	// ADSB.Enabled because it is a different service with a different privacy
	// implication, and identification works without it.
	ResolveAircraftDetail bool `yaml:"resolveaircraftdetail" json:"resolveAircraftDetail" mapstructure:"resolveaircraftdetail"`
}

// DefaultSoundNetSettings returns the shipped defaults.
//
// The station elevation and the credentials path are prepopulated with this
// deployment's known values, so the settings page opens with something real
// rather than zeroes an operator has to decode. Everything that costs cycles or
// makes a network call is still off.
func DefaultSoundNetSettings() SoundNetSettings {
	return SoundNetSettings{
		Enabled: false,
		Station: StationSettings{
			// The deployment station's surveyed elevation. Latitude and longitude
			// come from BirdNET.Latitude / BirdNET.Longitude.
			ElevationM: 140,
		},
		Diagnostics: DiagnosticsSettings{
			Enabled:   false,
			MaxClipMs: 5000,
		},
		Enrichment: EnrichmentSettings{
			Enabled: false,
			// Off. Corroboration spends an API credit on detections that would
			// otherwise have been dropped for free.
			CorroborationThreshold: 0,
			ADSB: ADSBSettings{
				Enabled:         false,
				CredentialsPath: "",
				SearchRadiusM:   12000,
				// 7km is roughly where the 20-second acoustic-lag cap lands, and
				// also about as far as an airliner is reliably audible.
				MaxRangeM:             7000,
				CreditFloor:           200,
				ResolveAircraftDetail: true,
			},
		},
	}
}

// setSoundNetDefaults registers the fork's viper defaults.
//
// Separate from upstream's setDefaults so the whole fork's default handling is
// one function call in that file rather than a block of lines interleaved with
// upstream's own.
func setSoundNetDefaults() {
	d := DefaultSoundNetSettings()
	viper.SetDefault("soundnet.enabled", d.Enabled)
	viper.SetDefault("soundnet.station.elevationm", d.Station.ElevationM)
	viper.SetDefault("soundnet.diagnostics.enabled", d.Diagnostics.Enabled)
	viper.SetDefault("soundnet.diagnostics.maxclipms", d.Diagnostics.MaxClipMs)
	viper.SetDefault("soundnet.diagnostics.calibratedmic", d.Diagnostics.CalibratedMic)
	viper.SetDefault("soundnet.diagnostics.referencesplat1m", d.Diagnostics.ReferenceSPLAt1m)
	viper.SetDefault("soundnet.enrichment.enabled", d.Enrichment.Enabled)
	viper.SetDefault("soundnet.enrichment.adsb.enabled", d.Enrichment.ADSB.Enabled)
	viper.SetDefault("soundnet.enrichment.adsb.credentialspath", d.Enrichment.ADSB.CredentialsPath)
	viper.SetDefault("soundnet.enrichment.adsb.searchradiusm", d.Enrichment.ADSB.SearchRadiusM)
	viper.SetDefault("soundnet.enrichment.adsb.maxrangem", d.Enrichment.ADSB.MaxRangeM)
	viper.SetDefault("soundnet.enrichment.adsb.creditfloor", d.Enrichment.ADSB.CreditFloor)
	viper.SetDefault("soundnet.enrichment.adsb.resolveaircraftdetail", d.Enrichment.ADSB.ResolveAircraftDetail)
}
