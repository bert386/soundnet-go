package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSoundNetDefaultsAreSafeAndPrepopulated(t *testing.T) {
	d := DefaultSoundNetSettings()

	// Everything that costs cycles or makes an outbound call must be off until
	// an operator turns it on. This is the scope's rule, not a preference.
	assert.False(t, d.Enabled)
	assert.False(t, d.Diagnostics.Enabled)
	assert.False(t, d.Enrichment.Enabled)
	assert.False(t, d.Enrichment.ADSB.Enabled)

	// But the values an operator would otherwise have to look up are filled in,
	// so the settings page opens with something real rather than zeroes.
	assert.InDelta(t, 140.0, d.Station.ElevationM, 0.001, "the surveyed station elevation")
	assert.Positive(t, d.Enrichment.ADSB.SearchRadiusM)
	assert.Positive(t, d.Enrichment.ADSB.MaxRangeM)
	assert.Positive(t, d.Enrichment.ADSB.CreditFloor,
		"a credit reserve must exist or the collector can starve runtime enrichment")
}

func TestSoundNetHasNoDuplicateLocation(t *testing.T) {
	// Latitude and longitude deliberately live on BirdNET, where upstream
	// already collects and edits them. Two places to set one fact would drift.
	var s Settings
	s.BirdNET.Latitude = -34.11159024409095
	s.BirdNET.Longitude = 150.7922555571461
	s.SoundNet = DefaultSoundNetSettings()

	assert.InDelta(t, -34.11159024409095, s.BirdNET.Latitude, 1e-9)
	assert.InDelta(t, 150.7922555571461, s.BirdNET.Longitude, 1e-9)
	assert.InDelta(t, 140.0, s.SoundNet.Station.ElevationM, 0.001,
		"only elevation is added, because upstream has no use for it")
}

func TestCredentialsAreAPathNotASecret(t *testing.T) {
	d := DefaultSoundNetSettings()
	// The field holds a path. A secret in a versioned config file ends up in
	// backups, support dumps and screenshots.
	assert.Empty(t, d.Enrichment.ADSB.CredentialsPath,
		"no credential is shipped; the operator supplies a path")
}
