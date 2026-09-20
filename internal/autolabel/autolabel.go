// Package autolabel builds a labelled aircraft audio corpus automatically.
//
// It is the runtime enrichment path run backwards. Instead of hearing something
// and asking ADS-B what it was, it watches ADS-B for an aircraft close enough to
// be clearly audible and captures the audio that must contain it, labelled with
// what the transponder says. Over weeks this accumulates a site-specific
// training set, which is how the AeroSonicDB dataset was built and what the M4
// aircraft-type head needs in order to exist at all.
//
// The standard for capturing is deliberately stricter than for identifying a
// detection at runtime. A wrong runtime identification is one wrong row; a wrong
// training label teaches the model something false, and every future prediction
// carries a little of that error. So where runtime matching accepts the best
// candidate above a threshold, this refuses anything it cannot attribute
// unambiguously.
package autolabel

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/bert386/soundnet-go/internal/enrichment"
)

// Aircraft is one candidate overflight, already corrected for acoustic lag.
type Aircraft struct {
	ICAO24     string
	Callsign   string
	AltitudeM  float64
	SlantM     float64
	Lag        time.Duration
	GroundSpMS float64
	TrackDeg   float64

	// Attributes carries whatever the metadata lookup resolved: registration,
	// type, operator, route. Absent keys mean not found, never "unknown".
	Attributes map[string]any
}

// Config controls when a capture is triggered.
type Config struct {
	// Enabled gates the whole collector. Off by default: it polls an external
	// API continuously and writes audio to disk.
	Enabled bool

	// PollInterval is how often the sky is checked. Every poll costs one API
	// credit against a daily allowance shared with runtime enrichment, so this
	// is the main lever on cost.
	PollInterval time.Duration

	// MaxSlantM is how close an aircraft must be to be confidently the loudest
	// thing in the clip. Tighter than the runtime matching range on purpose.
	MaxSlantM float64

	// MaxAltitudeM excludes high cruise traffic, which is often inaudible under
	// background noise even when geometrically close.
	MaxAltitudeM float64

	// MinSeparationM is how much further away the second-nearest aircraft must
	// be before the nearest can be considered the unambiguous source. Two
	// aircraft at similar range make attribution a guess.
	MinSeparationM float64

	// PerAircraftCooldown stops the same aircraft being captured repeatedly on
	// consecutive polls of a single overflight, which would fill the corpus with
	// near-duplicates of one event and bias training towards whatever flies the
	// same route daily.
	PerAircraftCooldown time.Duration

	// MaxCapturesPerHour bounds disk growth and keeps a busy corridor from
	// swamping the corpus in a single afternoon.
	MaxCapturesPerHour int

	// CreditReserve is the API credit floor. The collector stops polling before
	// the allowance is exhausted, so runtime enrichment - which only spends a
	// credit when something was actually heard - keeps working.
	CreditReserve int
}

// DefaultConfig returns a conservative configuration, disabled.
func DefaultConfig() Config {
	return Config{
		Enabled:             false,
		PollInterval:        30 * time.Second,
		MaxSlantM:           4000,
		MaxAltitudeM:        2500,
		MinSeparationM:      3000,
		PerAircraftCooldown: 10 * time.Minute,
		MaxCapturesPerHour:  20,
		CreditReserve:       500,
	}
}

// SkyReader reports which aircraft are currently near the station, with acoustic
// lag already applied.
type SkyReader interface {
	Overhead(ctx context.Context, station enrichment.Station, radiusM float64) ([]Aircraft, error)
	CreditsRemaining() (credits int, known bool)
}

// Capturer hands back the audio around a moment. The collector does not know how
// audio is buffered; it only asks for the window it wants.
type Capturer interface {
	// CaptureAround returns PCM covering the given instant, or an error if that
	// audio is no longer available.
	CaptureAround(ctx context.Context, at time.Time, window time.Duration) (pcm []byte, sampleRate, bitDepth, channels int, err error)
}

// CorpusWriter persists a labelled example.
type CorpusWriter interface {
	Write(ctx context.Context, sample *Sample) (path string, err error)
}

// Sample is one labelled training example.
type Sample struct {
	CapturedAt time.Time
	Aircraft   Aircraft

	PCM         []byte
	SampleRate  int
	BitDepth    int
	NumChannels int

	// Station is recorded with the sample because the labels are only meaningful
	// relative to where the microphone was.
	Station enrichment.Station

	// Reason records why this overflight qualified, so a corpus can be audited
	// later without re-deriving the geometry.
	Reason string
}

// Decision explains what the collector did with one poll, for logging and tests.
type Decision struct {
	Captured bool
	ICAO24   string
	SlantM   float64
	Skipped  string
}

// Collector watches the sky and captures labelled audio.
type Collector struct {
	Config  Config
	Sky     SkyReader
	Capture Capturer
	Corpus  CorpusWriter

	// OnDecision, when set, is called with the outcome of every poll.
	//
	// The collector is otherwise completely silent: it writes a file when it
	// captures and does nothing at all the rest of the time, so "no captures
	// yet" and "never ran" look identical from outside. That is the exact
	// failure this project has already had once, in the layer this one feeds.
	OnDecision func(*Decision)

	mu         sync.Mutex
	lastSeen   map[string]time.Time
	captureLog []time.Time
	nowFunc    func() time.Time
}

// New returns a collector.
func New(cfg Config, sky SkyReader, capture Capturer, corpus CorpusWriter) *Collector {
	return &Collector{
		Config:   cfg,
		Sky:      sky,
		Capture:  capture,
		Corpus:   corpus,
		lastSeen: make(map[string]time.Time),
		nowFunc:  time.Now,
	}
}

func (c *Collector) now() time.Time {
	if c.nowFunc != nil {
		return c.nowFunc()
	}
	return time.Now()
}

// Run polls until the context is cancelled.
func (c *Collector) Run(ctx context.Context, station enrichment.Station) error {
	if !c.Config.Enabled {
		return nil
	}
	if !station.Valid() {
		return fmt.Errorf("autolabel: %w: station position", enrichment.ErrNotConfigured)
	}
	interval := c.Config.PollInterval
	if interval <= 0 {
		interval = DefaultConfig().PollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			decision, err := c.Poll(ctx, station)
			if err != nil {
				// A failed poll is not fatal: the sky will still be there next
				// time, and stopping the collector over one network error would
				// silently end data collection for the day. Reported, though,
				// because a collector failing every poll for a day looks exactly
				// like a quiet sky.
				c.report(&Decision{Skipped: "poll failed: " + err.Error()})
				continue
			}
			c.report(decision)
		}
	}
}

// report hands a decision to the observer, if there is one.
func (c *Collector) report(d *Decision) {
	if c.OnDecision != nil && d != nil {
		c.OnDecision(d)
	}
}

// Poll checks the sky once and captures if an overflight qualifies.
func (c *Collector) Poll(ctx context.Context, station enrichment.Station) (*Decision, error) {
	if credits, known := c.Sky.CreditsRemaining(); known && credits <= c.Config.CreditReserve {
		// Stop before the allowance is gone. Runtime enrichment only spends a
		// credit when something was actually heard, so it is the more valuable
		// consumer of what remains.
		return &Decision{Skipped: "api credit reserve reached"}, nil
	}

	aircraft, err := c.Sky.Overhead(ctx, station, c.Config.MaxSlantM*2)
	if err != nil {
		return nil, fmt.Errorf("autolabel: read sky: %w", err)
	}

	best, decision := c.choose(aircraft)
	if best == nil {
		return decision, nil
	}

	at := c.now().Add(-best.Lag)
	pcm, rate, depth, channels, err := c.Capture.CaptureAround(ctx, at, 3*time.Second)
	if err != nil {
		// Returning nil here is deliberate, not an oversight. The aircraft really
		// was overhead; the audio simply was not retained long enough. That is an
		// ordinary miss rather than a failure, and surfacing it as an error would
		// stop the collector - silently ending data collection for the day over a
		// buffer that had already moved on.
		//nolint:nilerr // an expired buffer is a miss, not an error; see above
		return &Decision{ICAO24: best.ICAO24, Skipped: "audio unavailable: " + err.Error()}, nil
	}

	sample := &Sample{
		CapturedAt:  at,
		Aircraft:    *best,
		PCM:         pcm,
		SampleRate:  rate,
		BitDepth:    depth,
		NumChannels: channels,
		Station:     station,
		Reason:      decision.Skipped,
	}
	if _, err := c.Corpus.Write(ctx, sample); err != nil {
		return nil, fmt.Errorf("autolabel: write corpus: %w", err)
	}

	c.mu.Lock()
	c.lastSeen[best.ICAO24] = c.now()
	c.captureLog = append(c.captureLog, c.now())
	c.mu.Unlock()

	return &Decision{Captured: true, ICAO24: best.ICAO24, SlantM: best.SlantM}, nil
}

// choose picks the one aircraft that can be labelled without guessing.
func (c *Collector) choose(candidates []Aircraft) (*Aircraft, *Decision) {
	inRange := make([]Aircraft, 0, len(candidates))
	for i := range candidates {
		a := candidates[i]
		if a.SlantM <= c.Config.MaxSlantM && a.AltitudeM <= c.Config.MaxAltitudeM {
			inRange = append(inRange, a)
		}
	}
	if len(inRange) == 0 {
		return nil, &Decision{Skipped: "nothing close and low enough"}
	}

	// Nearest first.
	best := 0
	for i := range inRange {
		if inRange[i].SlantM < inRange[best].SlantM {
			best = i
		}
	}
	winner := inRange[best]

	// Ambiguity check. Two aircraft at similar range make the label a guess, and
	// a guessed training label is worse than no sample at all: it teaches the
	// model something false, and the error propagates into every prediction the
	// model later makes.
	for i := range inRange {
		if i == best {
			continue
		}
		if inRange[i].SlantM-winner.SlantM < c.Config.MinSeparationM {
			return nil, &Decision{
				ICAO24:  winner.ICAO24,
				Skipped: "two aircraft at similar range; attribution would be a guess",
			}
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if last, ok := c.lastSeen[winner.ICAO24]; ok && c.now().Sub(last) < c.Config.PerAircraftCooldown {
		// One overflight spans several polls. Capturing each would fill the
		// corpus with near-duplicates of a single event, and a daily scheduled
		// service would come to dominate the training set.
		return nil, &Decision{ICAO24: winner.ICAO24, Skipped: "already captured recently"}
	}

	if c.Config.MaxCapturesPerHour > 0 {
		cutoff := c.now().Add(-time.Hour)
		recent := 0
		kept := c.captureLog[:0]
		for _, t := range c.captureLog {
			if t.After(cutoff) {
				kept = append(kept, t)
				recent++
			}
		}
		c.captureLog = kept
		if recent >= c.Config.MaxCapturesPerHour {
			return nil, &Decision{ICAO24: winner.ICAO24, Skipped: "hourly capture limit reached"}
		}
	}

	return &winner, &Decision{
		ICAO24:  winner.ICAO24,
		SlantM:  winner.SlantM,
		Skipped: fmt.Sprintf("nearest at %.0fm, next is clear by %.0fm", winner.SlantM, c.Config.MinSeparationM),
	}
}
