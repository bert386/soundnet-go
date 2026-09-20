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
	AutoLabel   AutoLabelSettings   `yaml:"autolabel" json:"autoLabel" mapstructure:"autolabel"`
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

// AutoLabelSettings controls the ADS-B auto-labelling collector.
//
// This is the enrichment path run backwards. Instead of hearing something and
// asking ADS-B what it was, it watches for an aircraft close enough to be
// unmistakably audible and keeps the audio that must contain it, labelled with
// what the transponder said. Weeks of that is a site-specific training corpus,
// recorded through this microphone at this location - which is the only way a
// useful aircraft-type head can exist here.
//
// Off by default for two reasons rather than one. It polls an external API on a
// timer whether or not anything was heard, unlike runtime enrichment which only
// spends a credit on a detection; and it writes audio to disk continuously.
type AutoLabelSettings struct {
	Enabled bool `yaml:"enabled" json:"enabled" mapstructure:"enabled"`

	// CorpusDir is where labelled clips and their JSON sidecars are written,
	// relative to the binary unless absolute. Its own directory rather than the
	// clips folder: these are training data with a different lifetime from
	// detection clips, and the disk manager must not age them out.
	CorpusDir string `yaml:"corpusdir" json:"corpusDir" mapstructure:"corpusdir"`

	// SourceID names the audio source to record from. Empty means the only
	// configured source, which is the usual case. It must be set when a station
	// has several: a corpus that mixes microphones teaches a model the
	// difference between the microphones as readily as the difference between
	// aircraft, and nothing downstream would show that had happened.
	SourceID string `yaml:"sourceid" json:"sourceId" mapstructure:"sourceid"`

	// PollIntervalSec is how often the sky is checked. Every poll costs one API
	// credit, so this is the main lever on what the collector costs.
	PollIntervalSec int `yaml:"pollintervalsec" json:"pollIntervalSec" mapstructure:"pollintervalsec"`

	// MaxSlantM is how close an aircraft must be before its sound is assumed to
	// dominate the clip. Tighter than the runtime matching range on purpose:
	// runtime asks which aircraft best explains a sound that was definitely
	// heard, while this has no acoustic evidence at all and is relying on
	// geometry alone.
	MaxSlantM float64 `yaml:"maxslantm" json:"maxSlantM" mapstructure:"maxslantm"`

	// MaxAltitudeM excludes high cruise traffic, which is often inaudible under
	// background noise even when geometrically close.
	MaxAltitudeM float64 `yaml:"maxaltitudem" json:"maxAltitudeM" mapstructure:"maxaltitudem"`

	// MinSeparationM is how much further away the second-nearest aircraft must
	// be before the nearest can be called the unambiguous source. Two aircraft
	// at similar range make the label a guess, and a guessed training label is
	// worse than no sample: a wrong runtime match is one wrong row, while a
	// wrong label teaches the model something false permanently.
	MinSeparationM float64 `yaml:"minseparationm" json:"minSeparationM" mapstructure:"minseparationm"`

	// PerAircraftCooldownMin stops one overflight being captured on every poll
	// it spans, which would fill the corpus with near-duplicates of a single
	// event and let a daily scheduled service dominate the training set.
	PerAircraftCooldownMin int `yaml:"peraircraftcooldownmin" json:"perAircraftCooldownMin" mapstructure:"peraircraftcooldownmin"`

	// MaxCapturesPerHour bounds disk growth and keeps a busy corridor from
	// swamping the corpus in one afternoon.
	MaxCapturesPerHour int `yaml:"maxcapturesperhour" json:"maxCapturesPerHour" mapstructure:"maxcapturesperhour"`

	// CreditReserve is the API credit floor the collector stops at. Set higher
	// than Enrichment.ADSB.CreditFloor on purpose: the two share one daily
	// allowance, and the collector should run out first. It polls on a timer
	// whether or not anything flew, whereas runtime enrichment only spends a
	// credit when something was actually heard - so what remains is worth more
	// to runtime.
	CreditReserve int `yaml:"creditreserve" json:"creditReserve" mapstructure:"creditreserve"`
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
		AutoLabel: AutoLabelSettings{
			Enabled:   false,
			CorpusDir: "corpus/aircraft",
			// Thirty seconds is a compromise: an airliner crosses the capture
			// radius in well under a minute, so polling much slower misses
			// overflights entirely, and polling faster spends credits on a sky
			// that has barely changed.
			PollIntervalSec: 30,
			MaxSlantM:       4000,
			MaxAltitudeM:    2500,
			MinSeparationM:  3000,
			// Longer than a single overflight takes to cross the sky, so one
			// aircraft contributes one sample.
			PerAircraftCooldownMin: 10,
			MaxCapturesPerHour:     20,
			// Above the runtime floor of 200, so the collector stops first.
			CreditReserve: 500,
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
	viper.SetDefault("soundnet.autolabel.enabled", d.AutoLabel.Enabled)
	viper.SetDefault("soundnet.autolabel.corpusdir", d.AutoLabel.CorpusDir)
	viper.SetDefault("soundnet.autolabel.sourceid", d.AutoLabel.SourceID)
	viper.SetDefault("soundnet.autolabel.pollintervalsec", d.AutoLabel.PollIntervalSec)
	viper.SetDefault("soundnet.autolabel.maxslantm", d.AutoLabel.MaxSlantM)
	viper.SetDefault("soundnet.autolabel.maxaltitudem", d.AutoLabel.MaxAltitudeM)
	viper.SetDefault("soundnet.autolabel.minseparationm", d.AutoLabel.MinSeparationM)
	viper.SetDefault("soundnet.autolabel.peraircraftcooldownmin", d.AutoLabel.PerAircraftCooldownMin)
	viper.SetDefault("soundnet.autolabel.maxcapturesperhour", d.AutoLabel.MaxCapturesPerHour)
	viper.SetDefault("soundnet.autolabel.creditreserve", d.AutoLabel.CreditReserve)
}
