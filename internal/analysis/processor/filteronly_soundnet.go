package processor

// SOUNDNET: classes that exist only so a filter can see them must not become
// detections.
//
// The YAMNet adapter deliberately emits every class vocalization.IsHuman or
// IsDog recognises, on top of the event taxonomy. It has to: the privacy and
// dog-bark filters work by inspecting results as they pass through the
// processor, so a class the adapter withholds is a class they can never act on.
// Filtering YAMNet down to the taxonomy alone would silently weaken privacy
// protection, because speech is not an "event" and is not in the taxonomy.
//
// The comment on reportable() then claims emitting them "costs nothing in
// stored rows", because a speech hit makes the privacy filter discard the whole
// window and the speech result goes with it. That is true only *above* the
// privacy threshold. Below it the window is kept, nothing drops the class, and
// it is stored as an ordinary detection - with no domain, no display name and a
// mangled label. The operator has reviewed four of them as "rustling dry grass"
// and one as "human cough", which is the point: they were never events, and at
// 0.41-0.59 they were never going to trip the privacy filter either.
//
// So they are dropped here, after both filters have run and before anything is
// stored. Dropped rather than never emitted, because the filters still need to
// see them.

import (
	"github.com/bert386/soundnet-go/internal/classifier"
	"github.com/bert386/soundnet-go/internal/eventclass"
	"github.com/bert386/soundnet-go/internal/labels/vocalization"
)

// soundNetEmitsFilterInputs reports whether a model's adapter deliberately emits
// classes beyond the event taxonomy for the filters' benefit.
//
// Scoped to the models this fork adds. Upstream decides what BirdNET's own
// non-species labels do, and quietly changing that for every BirdNET-Go user
// would be a different change than the one being made here.
func soundNetEmitsFilterInputs(modelID string) bool {
	return modelID == classifier.RegistryIDYAMNet
}

// soundNetFilterOnlyClass reports whether this result exists only to feed the
// privacy or dog-bark filter, and should therefore not be stored.
//
// rawLabel is the classifier's own label, which is what both vocalization and
// the event taxonomy key on.
func soundNetFilterOnlyClass(modelID, rawLabel string) bool {
	if !soundNetEmitsFilterInputs(modelID) {
		return false
	}
	if !vocalization.IsHuman(rawLabel) && !vocalization.IsDog(rawLabel) {
		// Not a filter input at all; it is in the emit set on the taxonomy's
		// account and belongs in the detection list.
		return false
	}
	// A class can be both: "Dog" is recognised by IsDog *and* carried by the
	// taxonomy as a default-enabled biological event. A barking dog is a real
	// event worth recording, so taxonomy membership wins.
	class, found := eventclass.Resolve(rawLabel)
	return !found || !class.DefaultEnabled
}
