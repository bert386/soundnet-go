package processor

// SOUNDNET: keeping a quiet detection because an authority independently says
// something was there.
//
// The problem this solves is one of ordering rather than of accuracy. On the
// operator-labelled set, four of seven confirmed aircraft score under 0.30 in
// every model and preprocessing configuration tried - they are the distant ones,
// and neither a 2023 tagger nor a low-pass second pass rescued them. A sensible
// threshold is 0.7, so no detection is created for them; and because no
// detection is created, nothing ever reaches the enrichment layer to discover
// that an aircraft really was overhead. The evidence that would justify keeping
// the detection is unreachable from behind the threshold that discards it.
//
// So a detection in an enrichable domain scoring at least
// soundnet.enrichment.corroborationthreshold is held to the end of its pending
// window and kept only if an authority places a credible source overhead at that
// moment. This is deliberately not "a lower threshold for aircraft": nothing is
// admitted on the acoustic score alone, so a quiet afternoon produces no rows at
// all rather than a list of hopeful guesses.
//
// The asymmetry is the point, and it is the scope's rule rather than a new one.
// 0.15 on its own is noise. 0.15 beside an ADS-B contact at 1.6 km is a
// detection, and ADS-B will not corroborate rustling grass no matter how much of
// it there is.

import (
	"context"
	"time"

	"github.com/bert386/soundnet-go/internal/conf"
	"github.com/bert386/soundnet-go/internal/eventclass"
	"github.com/bert386/soundnet-go/internal/logger"
)

// soundNetCorroborationTimeout bounds the lookup.
//
// This runs inside the pending-detection flush, which holds p.pendingMutex, so a
// slow network call here stalls every other detection waiting to flush. Short on
// purpose: the honest outcome of a slow authority is an undetermined detection,
// which is discarded exactly as an uncorroborated one is.
const soundNetCorroborationTimeout = 3 * time.Second

// soundNetCorroborationThreshold returns the acoustic score below which a
// detection needs an authority to back it up, and whether the mechanism is on at
// all.
//
// Off unless SoundNet, enrichment and a positive threshold are all configured.
// Each is a separate opt-in and all three have to be deliberate: this is the
// only part of detection that can make an outbound call on a detection the user
// would otherwise never have seen.
func soundNetCorroborationThreshold(settings *conf.Settings) (threshold float32, enabled bool) {
	if settings == nil || !settings.SoundNet.Enabled || !settings.SoundNet.Enrichment.Enabled {
		return 0, false
	}
	t := settings.SoundNet.Enrichment.CorroborationThreshold
	if t <= 0 {
		return 0, false
	}
	return float32(t), true
}

// soundNetNeedsCorroboration reports whether this detection is only a candidate:
// admitted below the normal threshold, in a domain some authority can speak for.
//
// A detection that clears the ordinary threshold is an ordinary detection and is
// never sent for corroboration - it does not need it, and asking would spend a
// credit to learn nothing that changes the outcome.
func soundNetNeedsCorroboration(settings *conf.Settings, label string, confidence, normalThreshold float32) bool {
	candidateFloor, on := soundNetCorroborationThreshold(settings)
	if !on {
		return false
	}
	if confidence >= normalThreshold {
		return false
	}
	if confidence < candidateFloor {
		return false
	}
	class, _ := eventclass.Resolve(label)
	return class.Enrichable()
}

// soundNetCorroborates asks the authorities and reports whether to keep the
// detection.
//
// A provider that errors returns false, and that is the conservative direction:
// a broken network must not start admitting every quiet detection it cannot
// check. The inverse mistake - silently dropping the candidates while the
// operator believes corroboration is running - is guarded by logging the error
// rather than the absence.
func soundNetCorroborates(label string, at time.Time, confidence float32) bool {
	analyser := soundNetAnalyser
	if analyser == nil {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), soundNetCorroborationTimeout)
	defer cancel()

	result, err := analyser.Corroborate(ctx, label, at, float64(confidence))
	if err != nil {
		GetLogger().Warn("soundnet: corroboration lookup failed, candidate discarded",
			logger.String("label", label),
			logger.Error(err),
			logger.String("operation", "soundnet_corroboration_failed"))
		return false
	}
	if result == nil || !result.Matched {
		return false
	}

	GetLogger().Info("soundnet: quiet detection corroborated by an authority",
		logger.String("label", label),
		logger.Float64("confidence", float64(confidence)),
		logger.String("resolved_domain", string(result.Domain)),
		logger.String("provider", result.Provider),
		logger.String("operation", "soundnet_corroborated"))
	return true
}

// soundNetCandidateThreshold lowers the bar for a class an authority can speak
// for, and returns the threshold untouched for everything else.
//
// The pair of names is the stored scientific/common split, which for an event
// class is two thirds of a raw label rather than a species - eventclass.Resolve
// and DisplayName between them accept every form that reaches here.
func soundNetCandidateThreshold(settings *conf.Settings, scientificName, commonName string, threshold float32) float32 {
	candidateFloor, on := soundNetCorroborationThreshold(settings)
	if !on || candidateFloor >= threshold {
		return threshold
	}
	label := scientificName
	if display, ok := eventclass.DisplayName(scientificName, commonName); ok {
		label = display
	}
	class, found := eventclass.Resolve(label)
	if !found || !class.Enrichable() {
		return threshold
	}
	return candidateFloor
}

// soundNetDiscardUncorroborated drops a candidate that no authority vouched for.
//
// It runs at flush time rather than at detection time for two reasons. The
// pending window gives repeated hits a chance to accumulate, so a genuine
// overflight is usually asked about once rather than once per analysis window;
// and the acoustic lag correction wants the time the sound was heard, which the
// pending item already carries.
func (p *Processor) soundNetDiscardUncorroborated(item *PendingDetection, settings *conf.Settings) (discard bool, reason string) {
	return p.soundNetDiscardWith(item, settings, soundNetCorroborates)
}

// soundNetDiscardWith is soundNetDiscardUncorroborated with the lookup injected.
//
// The seam exists for the tests. soundNetCorroborates reads the package-level
// analyser, which ConfigureSoundNet writes once at startup and everything else
// only reads - safe in production, but a test that swapped it would be writing a
// global while parallel tests read it, which is a data race and was reported as
// one the first time this was written.
func (p *Processor) soundNetDiscardWith(
	item *PendingDetection,
	settings *conf.Settings,
	corroborate func(label string, at time.Time, confidence float32) bool,
) (discard bool, reason string) {
	if item == nil {
		return false, ""
	}
	normal := modelGlobalConfidenceThreshold(settings, item.BestModelID)
	label := soundNetLabel(&item.Detection.Result)
	if !soundNetNeedsCorroboration(settings, label, float32(item.Confidence), normal) {
		return false, ""
	}
	if corroborate(label, item.FirstDetected, float32(item.Confidence)) {
		return false, ""
	}
	GetLogger().Debug("soundnet: candidate discarded, nothing corroborated it",
		logger.String("label", label),
		logger.Float64("confidence", item.Confidence),
		logger.String("source", p.getDisplayNameForSource(item.Source)),
		logger.String("operation", "soundnet_uncorroborated"))
	return true, "no authority corroborated this quiet detection"
}
