package analysis

// SoundNet startup wiring.
//
// Builds the analyser from configuration and installs it in the processor. Kept
// in its own file so the fork's startup costs one call in an upstream file.

import (
	"encoding/json"
	"fmt"
	"os"

	"gorm.io/gorm"

	"github.com/bert386/soundnet-go/internal/acoustics"
	"github.com/bert386/soundnet-go/internal/analysis/processor"
	"github.com/bert386/soundnet-go/internal/conf"
	"github.com/bert386/soundnet-go/internal/datastore"
	"github.com/bert386/soundnet-go/internal/enrichment"
	"github.com/bert386/soundnet-go/internal/enrichment/adsb"
	"github.com/bert386/soundnet-go/internal/eventpipeline"
	"github.com/bert386/soundnet-go/internal/eventrecord"
	"github.com/bert386/soundnet-go/internal/logger"
)

// txStore satisfies eventpipeline.Store using only the datastore's public
// interface.
//
// The gorm handle itself is not exported by either datastore implementation, but
// Transaction is, and it hands out exactly what is needed. Routing every write
// through a transaction is slightly more ceremony than holding the handle, and
// it is also safer: a partially written diagnostics row cannot survive a crash.
type txStore struct {
	ds datastore.Interface
}

func (t txStore) PutDiagnostics(detectionID uint, doc any, schemaVersion int, computeMs int64) error {
	return t.ds.Transaction(func(tx *gorm.DB) error {
		return eventrecord.NewStore(tx).PutDiagnostics(detectionID, doc, schemaVersion, computeMs)
	})
}

func (t txStore) PutEnrichment(e *eventrecord.Enrichment) error {
	return t.ds.Transaction(func(tx *gorm.DB) error {
		return eventrecord.NewStore(tx).PutEnrichment(e)
	})
}

// initSoundNet builds and installs the SoundNet analyser.
//
// Every failure here is non-fatal and reported. SoundNet is an addition to bird
// detection, not a precondition for it: a missing ADS-B credential file should
// cost aircraft identification, not the whole service.
func initSoundNet(settings *conf.Settings, ds datastore.Interface, log logger.Logger) {
	if settings == nil || !settings.SoundNet.Enabled {
		return
	}
	cfg := settings.SoundNet

	// Schema first. Only tables in the soundnet_ namespace are touched, so this
	// cannot disturb upstream's schema or its migration bookkeeping.
	if ds != nil {
		if err := ds.Transaction(func(tx *gorm.DB) error {
			return eventrecord.NewStore(tx).Migrate()
		}); err != nil {
			log.Error("soundnet: schema migration failed, SoundNet disabled", logger.Error(err))
			return
		}
	}

	pipelineCfg := eventpipeline.DefaultConfig()
	pipelineCfg.DiagnosticsEnabled = cfg.Diagnostics.Enabled
	pipelineCfg.EnrichmentEnabled = cfg.Enrichment.Enabled
	pipelineCfg.Level = acoustics.DefaultLevelConfig()
	pipelineCfg.Level.Calibrated = cfg.Diagnostics.CalibratedMic
	pipelineCfg.Level.ReferenceSPLAt1m = cfg.Diagnostics.ReferenceSPLAt1m

	// The station reuses upstream's configured location; only elevation is ours.
	lat, lon, configured := settings.Location()
	pipelineCfg.Station = enrichment.Station{
		Latitude:   lat,
		Longitude:  lon,
		ElevationM: cfg.Station.ElevationM,
	}

	analyser := &eventpipeline.Analyser{Config: pipelineCfg, Store: txStore{ds: ds}}

	if cfg.Enrichment.Enabled {
		if !configured || !pipelineCfg.Station.Valid() {
			// Said plainly rather than left to look like a quiet sky. Without a
			// position there is no geometry and nothing can ever be identified.
			log.Warn("soundnet: enrichment enabled but no station location is configured; " +
				"set latitude and longitude in BirdNET settings and elevation in SoundNet settings")
		} else if registry := buildEnrichmentRegistry(&cfg, log); registry != nil {
			analyser.Resolver = registry
		}
	}

	processor.ConfigureSoundNet(analyser)
	log.Info("soundnet: enabled",
		logger.Bool("diagnostics", cfg.Diagnostics.Enabled),
		logger.Bool("enrichment", cfg.Enrichment.Enabled),
		logger.Float64("station_elevation_m", cfg.Station.ElevationM))
}

// buildEnrichmentRegistry assembles the configured identity providers.
func buildEnrichmentRegistry(cfg *conf.SoundNetSettings, log logger.Logger) *enrichment.Registry {
	registry := enrichment.NewRegistry()
	registered := 0

	if cfg.Enrichment.ADSB.Enabled {
		id, secret, err := readOpenSkyCredentials(cfg.Enrichment.ADSB.CredentialsPath)
		switch {
		case err != nil:
			// A configuration problem, reported as one. Anonymous OpenSky access
			// ignores the time parameter, so falling back to it would silently
			// disable the acoustic-lag correction rather than failing.
			log.Warn("soundnet: ADS-B enabled but credentials could not be read; aircraft identification disabled",
				logger.String("path", cfg.Enrichment.ADSB.CredentialsPath),
				logger.Error(err))
		default:
			client := adsb.NewOpenSkyClient(id, secret)
			client.CreditFloor = cfg.Enrichment.ADSB.CreditFloor

			provider := &adsb.Provider{
				Source: withFallback(client, &cfg.Enrichment.ADSB.Fallback, "runtime", log),
				Config: adsb.Config{
					SearchRadiusM:   cfg.Enrichment.ADSB.SearchRadiusM,
					MaxRangeM:       cfg.Enrichment.ADSB.MaxRangeM,
					MinQuality:      adsb.DefaultConfig().MinQuality,
					AmbiguityMargin: adsb.DefaultConfig().AmbiguityMargin,
				},
			}
			if cfg.Enrichment.ADSB.ResolveAircraftDetail {
				provider.Metadata = adsb.NewAdsbdbResolver()
			}
			registry.Register(provider)
			registered++
		}
	}

	if registered == 0 {
		return nil
	}
	return registry
}

// withFallback puts a backup position source behind OpenSky, when configured.
//
// Returns the client unchanged otherwise, so the station behaves exactly as it
// did. When it does chain, every pass-over is logged: a chain is otherwise
// silent, and a primary that had been failing for a week would look exactly
// like one that was working.
func withFallback(primary *adsb.OpenSkyClient, cfg *conf.ADSBFallbackSettings, consumer string, log logger.Logger) adsb.StateSource {
	if cfg == nil || !cfg.Enabled {
		return primary
	}
	backup := adsb.NewADSBLolClient()
	if cfg.BaseURL != "" {
		backup.BaseURL = cfg.BaseURL
	}
	log.Info("soundnet: ADS-B fallback enabled",
		logger.String("consumer", consumer),
		logger.String("primary", "opensky"),
		logger.String("fallback", backup.Name()))
	return &adsb.FallbackSource{
		Sources: []adsb.StateSource{primary, backup},
		OnFallback: func(source string, err error) {
			log.Info("soundnet: ADS-B source passed over, trying the next",
				logger.String("consumer", consumer),
				logger.String("source", source),
				logger.Error(err))
		},
	}
}

// readOpenSkyCredentials loads client credentials from a file.
//
// From a file rather than from configuration so the secret never enters a
// versioned config, a backup or a support dump.
func readOpenSkyCredentials(path string) (clientID, clientSecret string, err error) {
	if path == "" {
		return "", "", fmt.Errorf("no credentials path configured")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("read credentials: %w", err)
	}
	var creds struct {
		ClientID     string `json:"clientId"`
		ClientSecret string `json:"clientSecret"`
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		return "", "", fmt.Errorf("parse credentials: %w", err)
	}
	if creds.ClientID == "" || creds.ClientSecret == "" {
		return "", "", fmt.Errorf("credentials file is missing clientId or clientSecret")
	}
	return creds.ClientID, creds.ClientSecret, nil
}
