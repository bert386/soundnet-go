package nonbird

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bert386/soundnet-go/internal/eventclass"
)

// SoundNet: these tests guard the join between YAMNet's AudioSet labels and the
// non-bird class table. The failure they prevent is silent - a label missing
// here takes the species branch in the datastore and is stored with the Aves
// taxonomic class, so an aircraft is filed as a bird and nothing reports an
// error.
//
// Internal tests, because the additions have to be checked against the
// unexported `classes` and `firstTokenSet`. Importing internal/eventclass is
// safe in both directions: it depends on nothing in this repo.

// normaliseAudioSet converts an AudioSet display name to the raw-label form the
// models emit: lower case, comma-separated parts joined with "_and_", spaces as
// underscores, e.g. "Child speech, kid speaking" -> "child_speech_and_kid_speaking".
func normaliseAudioSet(displayName string) string {
	parts := strings.Split(displayName, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return strings.ToLower(strings.ReplaceAll(strings.Join(parts, "_and_"), " ", "_"))
}

func TestNormalisationMatchesUpstreamKeys(t *testing.T) {
	t.Parallel()
	// Pinned against keys upstream wrote by hand. Without this, a wrong rule
	// would derive every fork key wrongly in the same way, and the coverage test
	// below would pass while matching nothing at runtime.
	for display, want := range map[string]string{
		"Child speech, kid speaking":   "child_speech_and_kid_speaking",
		"Car passing by":               "car_passing_by",
		"Chirp, tweet":                 "chirp_and_tweet",
		"Bathtub (filling or washing)": "bathtub_(filling_or_washing)",
		"Accelerating, revving, vroom": "accelerating_and_revving_and_vroom",
		"Motor vehicle (road)":         "motor_vehicle_(road)",
	} {
		assert.Equal(t, want, normaliseAudioSet(display), "normalising %q", display)
		assert.Contains(t, classes, want, "%q should already be an upstream key", want)
	}
}

// TestEveryMappedAudioSetClassIsKnown is the test that matters: every class the
// event taxonomy can emit must resolve as non-bird.
//
// Driven off the taxonomy itself rather than a copied index list, so adding a
// class in eventclass without categorising it here fails immediately instead of
// misfiling detections in production.
func TestEveryMappedAudioSetClassIsKnown(t *testing.T) {
	t.Parallel()

	var missing []string
	checked := 0
	for _, domain := range eventclass.AllDomains() {
		for _, c := range eventclass.InDomain(domain) {
			if c.AudioSetIndex < 0 {
				// A classifier's own label rather than an AudioSet class;
				// upstream's own table is the authority on those.
				continue
			}
			checked++
			key := normaliseAudioSet(c.Label)
			if _, ok := CategoryOf(key); !ok {
				missing = append(missing, key+"  <- "+string(domain)+" "+c.Label)
			}
		}
	}

	assert.Positive(t, checked, "the taxonomy should map some AudioSet classes")
	assert.Emptyf(t, missing,
		"%d of %d mapped classes would be stored as bird species; add them to soundNetClasses:\n  %s",
		len(missing), checked, strings.Join(missing, "\n  "))
}

// TestMultiWordAdditionsReachFirstTokenSet proves init ordering does not matter.
// firstTokenSet is derived from `classes` by a different file's init; if this
// fork's additions landed after it and nothing rebuilt the set, every multi-word
// class added here would be invisible to IsNonBirdName - silently, and only for
// the fork's own classes.
func TestMultiWordAdditionsReachFirstTokenSet(t *testing.T) {
	t.Parallel()
	for _, token := range []string{"jet", "civil", "machine", "heavy", "propeller"} {
		_, ok := firstTokenSet[token]
		assert.Truef(t, ok,
			"first token %q missing; firstTokenSet was not rebuilt after the SoundNet additions", token)
	}
	assert.True(t, IsNonBirdName("jet"), "a truncated first token must resolve as non-bird")
}

func TestAdditionsDoNotOverrideUpstream(t *testing.T) {
	t.Parallel()
	// Extending a shared map can silently reclassify an upstream label. Adding a
	// key is fine; changing one behind upstream's back is not.
	for label := range soundNetClasses {
		assert.NotContains(t, upstreamOnlyLabels, label,
			"%q is already categorised upstream; do not redefine it here", label)
	}
}

// upstreamOnlyLabels names the upstream keys these additions sit closest to, so
// an accidental redefinition is caught rather than assumed away. Deliberately
// short and specific rather than a snapshot of all 198.
var upstreamOnlyLabels = map[string]struct{}{
	"aircraft": {}, "siren": {}, "alarm": {}, "engine": {}, "vehicle": {},
	"truck": {}, "explosion": {}, "fireworks": {}, "gunshot_and_gunfire": {},
	"power_tool": {}, "rain": {}, "wind": {}, "train": {}, "motorcycle": {},
}
