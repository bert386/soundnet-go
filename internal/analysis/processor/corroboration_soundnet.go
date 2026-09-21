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
	"strings"
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

// soundNetNeedsSecondOpinion reports whether every model that heard this
// detection is one the operator has said not to trust on its own.
//
// Only event classes are affected. A bird, or the speech label the privacy
// filter keys on, does not resolve in the event taxonomy and passes straight
// through - this must never be able to weaken the privacy filter or lose a bird.
//
// Agreement is read from the pending detection's per-model contributions, which
// merge every model's hits for the same label on the same source. A trusted
// model agrees only if its own best score would have been recorded on its own.
//
// Merely contributing is not enough, and the first version made exactly that
// mistake. For a class an authority can confirm, the admission bar is lowered to
// the corroboration floor, so CED's Thunderstorm at 0.2 on a windy clip is
// admitted as a candidate - and was then counted as agreeing with YAMNet's 0.92.
// The wind the rule exists for would have sailed through on a score CED itself
// would never have recorded.
func soundNetNeedsSecondOpinion(settings *conf.Settings, item *PendingDetection, label string) bool {
	if settings == nil || !settings.SoundNet.Enabled || item == nil {
		return false
	}
	distrusted := settings.SoundNet.Enrichment.RequireSecondOpinion
	if len(distrusted) == 0 {
		return false
	}
	if _, found := eventclass.Resolve(label); !found {
		return false
	}

	isDistrusted := func(model string) bool {
		for _, d := range distrusted {
			if strings.EqualFold(strings.TrimSpace(d), model) {
				return true
			}
		}
		return false
	}

	if len(item.ModelContributions) == 0 {
		// Older pending entries, or a single-model path that never filled the
		// map: the best model is then the only model.
		return item.BestModelID != "" && isDistrusted(item.BestModelID)
	}
	// A distrusted model has to be among those that heard it. Without this, a
	// detection only CED heard, weakly, fell through the loop and was discarded
	// in YAMNet's name - caught on the station's first live run, a CED Cat at
	// 0.32 logged as "only a model needing a second opinion heard it". CED's own
	// weak detections are CED's thresholds' business, not this rule's.
	heardByDistrusted := false
	for model, contribution := range item.ModelContributions {
		if isDistrusted(model) {
			heardByDistrusted = true
			continue
		}
		if float32(contribution.MaxConfidence) >= soundNetAgreementThreshold(settings, label, model) {
			return false
		}
	}
	return heardByDistrusted
}

// soundNetAgreementThreshold is the bar a model's score must clear for that model
// to have recorded this label by itself: the per-species setting if there is
// one, otherwise the model's threshold with SoundNet's domain and model
// overrides applied. Deliberately not the corroboration floor.
func soundNetAgreementThreshold(settings *conf.Settings, label, model string) float32 {
	if cfg, ok := lookupSpeciesConfig(settings.Realtime.Species.Config, label, label); ok {
		return float32(cfg.Threshold)
	}
	return soundNetThresholdOverride(settings, label, label, model,
		modelGlobalConfidenceThreshold(settings, model))
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
	var corroborate corroborateFunc = soundNetCorroborates
	if soundNetRejectsClipped(settings) {
		// Only consulted when an authority would otherwise be asked, so the
		// audio is read back for candidates, not for every detection.
		corroborate = rejectingClipped(
			func() (bool, bool) { return p.soundNetClipped(item) },
			corroborate,
			p.logClipRejection(item),
		)
	}
	return p.soundNetDiscardWith(item, settings, corroborate)
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
	candidate := soundNetNeedsCorroboration(settings, label, float32(item.Confidence), normal)
	alone := soundNetNeedsSecondOpinion(settings, item, label)
	if !candidate && !alone {
		return false, ""
	}

	// Heard only by a model the operator does not trust alone, in a class no
	// authority can speak for: nothing can vouch for it, so there is nothing to
	// ask. This is the cockatoo recorded as Cat 0.97.
	if alone {
		if class, found := eventclass.Resolve(label); found && !class.Enrichable() {
			GetLogger().Info("soundnet: discarded, only a model needing a second opinion heard it",
				logger.String("label", label),
				logger.Float64("confidence", item.Confidence),
				logger.String("model", item.BestModelID),
				logger.String("source", p.getDisplayNameForSource(item.Source)),
				logger.String("operation", "soundnet_second_opinion"))
			return true, "only a model needing a second opinion heard this"
		}
	}

	if corroborate(label, item.FirstDetected, float32(item.Confidence)) {
		return false, ""
	}
	if alone {
		// Logged at info, unlike the ordinary candidate discard: these are
		// detections that would have been recorded before this rule existed,
		// so an operator wondering where their thunder went needs to see it.
		GetLogger().Info("soundnet: discarded, only a model needing a second opinion heard it and no authority confirmed it",
			logger.String("label", label),
			logger.Float64("confidence", item.Confidence),
			logger.String("model", item.BestModelID),
			logger.String("source", p.getDisplayNameForSource(item.Source)),
			logger.String("operation", "soundnet_second_opinion"))
		return true, "only a model needing a second opinion heard this, and no authority confirmed it"
	}
	GetLogger().Debug("soundnet: candidate discarded, nothing corroborated it",
		logger.String("label", label),
		logger.Float64("confidence", item.Confidence),
		logger.String("source", p.getDisplayNameForSource(item.Source)),
		logger.String("operation", "soundnet_uncorroborated"))
	return true, "no authority corroborated this quiet detection"
}
