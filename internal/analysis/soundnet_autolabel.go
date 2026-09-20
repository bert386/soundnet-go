package analysis

// SoundNet M6 wiring: the ADS-B auto-labelling collector.
//
// internal/autolabel holds the decision logic and the three interfaces it needs;
// this file supplies the three implementations and starts the goroutine.
//
// Separate from soundnet_startup.go because it starts later. The analyser can be
// installed as soon as the datastore exists, but the collector reads the capture
// buffer, which only exists once the audio pipeline has added its sources.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bert386/soundnet-go/internal/audiocore/buffer"
	"github.com/bert386/soundnet-go/internal/autolabel"
	"github.com/bert386/soundnet-go/internal/conf"
	"github.com/bert386/soundnet-go/internal/enrichment"
	"github.com/bert386/soundnet-go/internal/enrichment/adsb"
	"github.com/bert386/soundnet-go/internal/logger"
)

// Capture buffers are allocated mono 16-bit by the audio engine
// (defaultChannels and defaultBitDepth in audiocore/engine). Only the sample
// rate varies with the device, and the buffer itself reports that.
const (
	captureBitDepth = 16
	captureChannels = 1

	// captureGuard holds the requested window clear of the write head.
	// ReadSegment refuses a window extending past what has been written and will
	// not wait for it, so asking for audio right up to "now" is a race with the
	// capture goroutine that the collector would lose intermittently.
	captureGuard = 250 * time.Millisecond
)

// bufferCapturer satisfies autolabel.Capturer from the live capture buffer.
//
// The buffer is looked up per capture rather than held, because a source that is
// restarted - by the watchdog, or by the quiet-hours scheduler - gets a new
// buffer, and a held pointer would go on reading a buffer nothing writes to.
type bufferCapturer struct {
	mgr      *buffer.Manager
	sourceID string
}

func (b bufferCapturer) CaptureAround(_ context.Context, at time.Time, window time.Duration) (pcm []byte, sampleRate, bitDepth, channels int, err error) {
	if b.mgr == nil {
		return nil, 0, 0, 0, fmt.Errorf("autolabel: no buffer manager")
	}
	cb, err := b.mgr.CaptureBuffer(b.sourceID)
	if err != nil {
		return nil, 0, 0, 0, fmt.Errorf("autolabel: capture buffer for %q: %w", b.sourceID, err)
	}

	start, end := captureWindow(at, window, time.Now())
	segment, err := cb.ReadSegment(start, end)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	return segment, cb.SampleRate(), captureBitDepth, captureChannels, nil
}

// captureWindow centres a window on an instant and keeps it in the past.
//
// The window slides back rather than being truncated when it would overrun the
// write head: a training clip half a second early still contains the
// overflight, while a clip half the length is a different kind of example from
// the rest of the corpus.
func captureWindow(at time.Time, window time.Duration, now time.Time) (start, end time.Time) {
	start = at.Add(-window / 2)
	end = start.Add(window)
	if latest := now.Add(-captureGuard); end.After(latest) {
		end = latest
		start = end.Add(-window)
	}
	return start, end
}

// openSkySky satisfies autolabel.SkyReader.
//
// It is the runtime provider's geometry without its scoring. Resolve answers
// "which single aircraft best explains this sound"; the collector asks the
// different question "what is close enough right now to be worth recording",
// and applies its own, stricter, unambiguity test afterwards.
type openSkySky struct {
	states  adsb.StateSource
	credits func() (credits int, known bool)

	// metadata turns a hex code into a type designator, which is what the corpus
	// filing system is built on. Optional: without it every sample lands in
	// _untyped, which is still a usable example of an aircraft.
	metadata adsb.MetadataResolver

	// metadataWithinM bounds the third-party lookup to aircraft the collector
	// might actually capture. The search box is deliberately wider than the
	// capture range, and looking up every contact in it would query a free
	// community database for traffic that is never recorded.
	metadataWithinM float64
}

func (s *openSkySky) CreditsRemaining() (credits int, known bool) {
	if s.credits == nil {
		return 0, false
	}
	return s.credits()
}

func (s *openSkySky) Overhead(ctx context.Context, station enrichment.Station, radiusM float64) ([]autolabel.Aircraft, error) {
	if s.states == nil {
		return nil, fmt.Errorf("autolabel: no state source configured")
	}
	latMin, lonMin, latMax, lonMax := adsb.BoundingBox(station, radiusM)
	states, err := s.states.StatesInBox(ctx, latMin, lonMin, latMax, lonMax)
	if err != nil {
		return nil, fmt.Errorf("autolabel: fetch states: %w", err)
	}

	out := make([]autolabel.Aircraft, 0, len(states))
	for i := range states {
		st := &states[i]
		if !st.HasPosition || st.OnGround || st.AltitudeSource == "none" {
			// Ground traffic and positionless contacts cannot be the source of an
			// airborne sound, and without an altitude there is no slant range.
			continue
		}
		reported := enrichment.Position{
			Latitude:      st.Latitude,
			Longitude:     st.Longitude,
			AltitudeM:     st.AltitudeM(),
			GroundSpeedMS: st.VelocityMS,
			TrackDeg:      st.TrackDeg,
		}
		emitted, lag := enrichment.CorrectForAcousticLag(station, reported)
		if !enrichment.LagIsCredible(lag) {
			// Past this range the back-projection can be kilometres out. At
			// runtime that risks a wrong row; here it would write a wrong label
			// into the training set, where the error never expires.
			continue
		}

		aircraft := autolabel.Aircraft{
			ICAO24:     st.ICAO24,
			Callsign:   strings.TrimSpace(st.Callsign),
			AltitudeM:  st.AltitudeM(),
			SlantM:     enrichment.SlantRangeM(station, emitted),
			Lag:        lag,
			GroundSpMS: st.VelocityMS,
			TrackDeg:   st.TrackDeg,
			Attributes: map[string]any{},
		}
		s.attachMetadata(ctx, &aircraft)
		out = append(out, aircraft)
	}
	return out, nil
}

// attachMetadata fills in what the transponder does not broadcast.
//
// Failure is silent on purpose, and nothing is written as an empty string: an
// absent key means "not looked up or not found", which the corpus writer files
// under _untyped. A blank type_code would instead create a directory named for
// nothing and quietly separate those samples from the untyped ones they belong
// with.
func (s *openSkySky) attachMetadata(ctx context.Context, a *autolabel.Aircraft) {
	if s.metadata == nil || a.SlantM > s.metadataWithinM {
		return
	}
	info, err := s.metadata.Aircraft(ctx, a.ICAO24)
	if err != nil || info == nil {
		return
	}
	put := func(key, value string) {
		if value != "" {
			a.Attributes[key] = value
		}
	}
	put("type_code", info.TypeCode)
	put("type_name", info.TypeName)
	put("registration", info.Registration)
	put("manufacturer", info.Manufacturer)
	put("operator", info.Operator)
}

// startSoundNetAutoLabel launches the collector, or explains why it did not.
//
// Every refusal is logged rather than returned. The collector is an addition to
// the station, not a precondition for it, and a misconfigured corpus directory
// must not stop audio capture - but a collector that silently never runs is
// indistinguishable from a sky with no aircraft in it, which is the failure this
// project has already made once.
func startSoundNetAutoLabel(settings *conf.Settings, mgr *buffer.Manager, sourceIDs []string, done <-chan struct{}, wg *sync.WaitGroup, log logger.Logger) {
	if settings == nil || !settings.SoundNet.Enabled || !settings.SoundNet.AutoLabel.Enabled {
		return
	}
	cfg := settings.SoundNet

	lat, lon, configured := settings.Location()
	station := enrichment.Station{Latitude: lat, Longitude: lon, ElevationM: cfg.Station.ElevationM}
	if !configured || !station.Valid() {
		log.Warn("soundnet: auto-labelling enabled but no station location is configured; collector not started")
		return
	}

	if !cfg.Enrichment.ADSB.Enabled {
		// The collector is ADS-B run backwards; without it there is no label.
		log.Warn("soundnet: auto-labelling enabled but ADS-B is not; collector not started")
		return
	}
	id, secret, err := readOpenSkyCredentials(cfg.Enrichment.ADSB.CredentialsPath)
	if err != nil {
		log.Warn("soundnet: auto-labelling enabled but OpenSky credentials could not be read; collector not started",
			logger.String("path", cfg.Enrichment.ADSB.CredentialsPath),
			logger.Error(err))
		return
	}

	sourceID, err := autoLabelSourceID(cfg.AutoLabel.SourceID, sourceIDs)
	if err != nil {
		log.Warn("soundnet: auto-labelling cannot choose an audio source; collector not started",
			logger.Error(err))
		return
	}

	corpusDir := strings.TrimSpace(cfg.AutoLabel.CorpusDir)
	if corpusDir == "" {
		log.Warn("soundnet: auto-labelling has no corpus directory configured; collector not started")
		return
	}

	// A separate OpenSky client from the runtime provider's, deliberately. Both
	// learn the account's true remaining balance from the rate-limit headers on
	// their own responses, so they do not need to share state - and a separate
	// client lets the collector hold a higher reserve than runtime enrichment,
	// which is what makes it stop first and leave credits for sounds that were
	// actually heard.
	client := adsb.NewOpenSkyClient(id, secret)
	client.CreditFloor = cfg.AutoLabel.CreditReserve

	sky := &openSkySky{
		states:          client,
		credits:         client.CreditsRemaining,
		metadataWithinM: cfg.AutoLabel.MaxSlantM,
	}
	if cfg.Enrichment.ADSB.ResolveAircraftDetail {
		sky.metadata = adsb.NewAdsbdbResolver()
	}

	collector := autolabel.New(
		autoLabelConfig(&cfg.AutoLabel),
		sky,
		bufferCapturer{mgr: mgr, sourceID: sourceID},
		&autolabel.FileCorpus{Root: conf.GetBasePath(corpusDir)},
	)
	collector.OnDecision = autoLabelDecisionLogger(log, sky)

	ctx, cancel := context.WithCancel(context.Background())
	wg.Go(func() {
		<-done
		cancel()
	})
	wg.Go(func() {
		defer cancel()
		if err := collector.Run(ctx, station); err != nil && !errors.Is(err, context.Canceled) {
			log.Error("soundnet: auto-label collector stopped", logger.Error(err))
		}
	})

	log.Info("soundnet: auto-label collector started",
		logger.String("source_id", sourceID),
		logger.String("corpus_dir", corpusDir),
		logger.Float64("max_slant_m", cfg.AutoLabel.MaxSlantM),
		logger.Float64("max_altitude_m", cfg.AutoLabel.MaxAltitudeM),
		logger.Int("poll_interval_s", cfg.AutoLabel.PollIntervalSec),
		logger.Int("credit_reserve", cfg.AutoLabel.CreditReserve))
}

// autoLabelDecisionLogger reports what the collector did with each poll.
//
// A capture is worth an info line: it is a new training example and there are at
// most twenty an hour. A refusal is debug, because there is one a minute and
// most of them say the same thing - nothing was close enough. But they are
// logged at all, and counted, because the two states that matter most to an
// operator are indistinguishable without them: a collector that is running and
// finding nothing, and a collector that is not running.
func autoLabelDecisionLogger(log logger.Logger, sky autolabel.SkyReader) func(*autolabel.Decision) {
	var polls, captures int64
	// The credit balance, which nothing recorded until it ran out. On the first
	// night the collector spent its whole allowance in eight hours and stopped,
	// and all that could be said afterwards was that 480 polls had been too
	// many - the number it was too many *of* was never written down. The client
	// learns the true balance from the rate-limit header on every response, so
	// it costs nothing to report.
	credits := func() logger.Field {
		if n, known := sky.CreditsRemaining(); known {
			return logger.Int64("credits_left", int64(n))
		}
		return logger.String("credits_left", "unknown")
	}
	return func(d *autolabel.Decision) {
		polls++
		switch {
		case d.Captured:
			captures++
			log.Info("soundnet: captured a labelled overflight",
				logger.String("icao24", d.ICAO24),
				logger.Float64("slant_m", d.SlantM),
				logger.Int64("captures", captures),
				logger.Int64("polls", polls),
				credits())
		case polls <= autoLabelOpeningPolls || polls%autoLabelHeartbeatPolls == 0:
			// The opening polls are info, then hourly. Debug alone was not
			// enough: the station runs at info, so the first version of this
			// left an hour between starting and the first sign of life - which
			// is the ambiguity it was written to remove.
			log.Info("soundnet: auto-label collector still watching",
				logger.Int64("polls", polls),
				logger.Int64("captures", captures),
				logger.String("last_reason", d.Skipped),
				credits())
		default:
			log.Debug("soundnet: no capture this poll",
				logger.String("reason", d.Skipped),
				logger.String("icao24", d.ICAO24))
		}
	}
}

// autoLabelHeartbeatPolls is how many polls pass between info lines. At the
// default one-minute interval that is roughly hourly.
const autoLabelHeartbeatPolls = 60

// autoLabelOpeningPolls is how many polls are reported at info on startup, so
// an operator can tell within a few minutes that the collector is alive and
// what it is deciding.
const autoLabelOpeningPolls = 3

// autoLabelConfig translates the settings into the collector's own config.
func autoLabelConfig(s *conf.AutoLabelSettings) autolabel.Config {
	cfg := autolabel.DefaultConfig()
	cfg.Enabled = true
	if s.PollIntervalSec > 0 {
		cfg.PollInterval = time.Duration(s.PollIntervalSec) * time.Second
	}
	if s.MaxSlantM > 0 {
		cfg.MaxSlantM = s.MaxSlantM
	}
	if s.MaxAltitudeM > 0 {
		cfg.MaxAltitudeM = s.MaxAltitudeM
	}
	if s.MinSeparationM > 0 {
		cfg.MinSeparationM = s.MinSeparationM
	}
	if s.PerAircraftCooldownMin > 0 {
		cfg.PerAircraftCooldown = time.Duration(s.PerAircraftCooldownMin) * time.Minute
	}
	if s.MaxCapturesPerHour > 0 {
		cfg.MaxCapturesPerHour = s.MaxCapturesPerHour
	}
	if s.CreditReserve > 0 {
		cfg.CreditReserve = s.CreditReserve
	}
	return cfg
}

// autoLabelSourceID decides which microphone the corpus is recorded from.
//
// It refuses to guess between several. A corpus that mixes microphones teaches a
// model the difference between the microphones as readily as the difference
// between aircraft, and nothing downstream would show that had happened.
func autoLabelSourceID(configured string, available []string) (string, error) {
	configured = strings.TrimSpace(configured)
	if configured != "" {
		for _, id := range available {
			if id == configured {
				return id, nil
			}
		}
		return "", fmt.Errorf("configured source %q is not an active audio source", configured)
	}
	switch len(available) {
	case 0:
		return "", fmt.Errorf("no active audio sources")
	case 1:
		return available[0], nil
	default:
		sorted := slices.Clone(available)
		slices.Sort(sorted)
		return "", fmt.Errorf("several audio sources are active (%s); set soundnet.autolabel.sourceid to choose one",
			strings.Join(sorted, ", "))
	}
}
