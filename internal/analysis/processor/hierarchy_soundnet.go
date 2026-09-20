package processor

// SOUNDNET: prefer the specific class over AudioSet's parent.
//
// AudioSet is a hierarchy, and `Vehicle` is the parent of `Aircraft`. The parent
// scores higher for the same sound - its training positives include every
// aircraft, car, train and boat - so an overflight is recorded as road traffic
// while the correct, specific class is recorded beside it at a lower score. The
// operator saw twenty-eight `Vehicle` rows in an evening, several of them
// aeroplanes ADS-B had already named.
//
// Measured on their labelled clips with the fused CED export the station runs,
// window by window rather than clip by clip, because one three-second window is
// all the decision has to go on (doc/soundnet/eval/hierarchy_windows.py):
//
//	216 windows, 108 from clips containing an aircraft and 108 from clips
//	containing none. The rule fires on 35 of the aircraft windows and on
//	**none** of the others. Where both floors are cleared, the child/parent
//	ratio on aircraft windows is 0.78 to 0.98; no negative window clears them
//	at all - including a clip of a large truck, where `Vehicle` reaches 0.693
//	and the best aircraft class 0.055.
//
// The separator is the child's own threshold, not the ratio: no negative window
// reached 0.15 on any aircraft class. The ratio is kept as a guard against the
// case the labelled corpus has only one example of - something genuinely a road
// vehicle that also weakly excites an aircraft class - and set from both
// datasets rather than one.
//
// The first value shipped, 0.5, came from the labelled clips alone, where true
// aircraft windows ran 0.78 to 0.98 of their parent. It was too high. A jet
// recorded the same evening and confirmed by ADS-B
// (doc/soundnet/eval/thunder_ratio.py) ran 0.35 to 0.82 with a median near
// 0.52, so half its windows were refused a correction the transponder then
// made anyway. The labelled set was not wrong, it was narrow: twelve clips
// chosen because a person could hear the aircraft in them.
//
// 0.25 is set between the two things actually measured: the truck, whose best
// aircraft class reached 0.055 against Vehicle 0.693 - a ratio of 0.08 - and
// the lowest confirmed aircraft window at 0.35.

import (
	"github.com/bert386/soundnet-go/internal/classifier"
	"github.com/bert386/soundnet-go/internal/conf"
	"github.com/bert386/soundnet-go/internal/datastore"
	"github.com/bert386/soundnet-go/internal/eventclass"
)

// soundNetSpecificRatio is how strong a child class must be relative to the
// parent before the parent is dropped. See the file comment for what it is
// measured against and why it is not zero.
const soundNetSpecificRatio = 0.25

// Known limit, found on the station rather than in the sweep: this compares
// results within one model's chunk, and the two readings of a sound are not
// always in the same model's. The Thunder and Thunderstorm rows at this station
// are YAMNet's, scoring 0.74 to 0.89, while CED - which hears the same audio -
// puts Thunderstorm at 0.000 and the aircraft classes at 0.15 to 0.38. No
// within-model rule can reconcile those two, because neither model holds both
// halves. ADS-B corroboration already resolves the domain for exactly these
// detections, so the row is correct even when its class name is not; carrying
// results between models would be a larger change than that is worth today.

// soundNetEventModel reports whether a model emits the AudioSet event taxonomy,
// and therefore whether its results can contain a parent/child pair at all.
//
// Scoped to the models this fork adds. BirdNET's own labels are upstream's to
// interpret, and silently reshaping them for every BirdNET-Go user would be a
// different change from the one being made here.
func soundNetEventModel(modelID string) bool {
	return modelID == classifier.RegistryIDYAMNet || modelID == classifier.RegistryIDCED
}

// soundNetPreferSpecific drops a parent-class result when a class from one of
// the domains it is ambiguous between fired on the same audio and will itself
// be recorded.
//
// "Will itself be recorded" is the condition that makes this safe. A parent is
// only dropped in favour of a child that clears its own threshold, so a sound
// the station detected can never become one it did not: in the worst case the
// specific row replaces the general one.
//
// parseAndValidateSpecies, which it sits between in the hot path
//
//nolint:gocritic // hugeParam: by value to match processResults and
func (p *Processor) soundNetPreferSpecific(settings *conf.Settings, item classifier.Results) []datastore.Results {
	return soundNetPreferSpecificWith(item.Results, item.ModelID, func(r datastore.Results) bool {
		// The real threshold, through the real parser. Deriving the names here
		// instead would mean reimplementing a splitter this project has already
		// got wrong once, in a place where being wrong is silent.
		sci, common, _, _, _ := p.parseAndValidateSpecies(settings, r, item)
		if sci == "" || common == "" {
			return false
		}
		return r.Confidence > p.getBaseConfidenceThreshold(settings, common, sci, item.ModelID)
	})
}

// soundNetPreferSpecificWith is the decision, separated from the processor so a
// test can drive it without standing up a classifier. admitted reports whether
// a result clears the threshold that would let it be stored.
//
// Returns the original slice when nothing is dropped, which is every chunk from
// every bird model and most chunks from the others.
func soundNetPreferSpecificWith(
	results []datastore.Results,
	modelID string,
	admitted func(datastore.Results) bool,
) []datastore.Results {
	if !soundNetEventModel(modelID) || len(results) < 2 {
		return results
	}

	// Resolve once per result: Resolve walks three label forms and this is the
	// detection hot path.
	classes := make([]eventclass.Class, len(results))
	found := make([]bool, len(results))
	ambiguous := false
	for i := range results {
		classes[i], found[i] = eventclass.Resolve(results[i].Species)
		if found[i] && eventclass.IsAmbiguous(classes[i].Label) {
			ambiguous = true
		}
	}
	if !ambiguous {
		return results
	}

	drop := make([]bool, len(results))
	dropped := 0
	for i := range results {
		if !found[i] || !eventclass.IsAmbiguous(classes[i].Label) {
			continue
		}
		if hasSpecificSibling(results, classes, found, i, admitted) {
			drop[i] = true
			dropped++
		}
	}
	if dropped == 0 {
		return results
	}

	kept := make([]datastore.Results, 0, len(results)-dropped)
	for i := range results {
		if !drop[i] {
			kept = append(kept, results[i])
		}
	}
	return kept
}

// hasSpecificSibling reports whether some other result in this chunk names one
// of the parent's candidate domains, clears its own threshold, and is strong
// enough relative to the parent to be believed.
func hasSpecificSibling(
	results []datastore.Results,
	classes []eventclass.Class,
	found []bool,
	parent int,
	admitted func(datastore.Results) bool,
) bool {
	candidates := classes[parent].CandidateDomains()
	if len(candidates) < 2 {
		return false
	}
	// The first candidate is the parent's own domain: a sibling from it is not
	// a more specific reading, it is the same one.
	wanted := make(map[eventclass.Domain]struct{}, len(candidates)-1)
	for _, d := range candidates[1:] {
		wanted[d] = struct{}{}
	}

	floor := float32(soundNetSpecificRatio) * results[parent].Confidence
	for i := range results {
		if i == parent || !found[i] {
			continue
		}
		if _, ok := wanted[classes[i].Domain]; !ok {
			continue
		}
		if results[i].Confidence < floor {
			continue
		}
		if admitted(results[i]) {
			return true
		}
	}
	return false
}
