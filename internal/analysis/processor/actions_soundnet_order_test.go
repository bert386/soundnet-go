package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bert386/soundnet-go/internal/conf"
	"github.com/bert386/soundnet-go/internal/eventpipeline"
)

// withSoundNetAnalyser installs an analyser for one test and puts the previous
// one back afterwards.
//
// It assigns the package variable directly rather than calling
// ConfigureSoundNet, which is sync.Once-guarded and therefore permanent. A test
// that configures SoundNet globally makes every later test in the package
// depend on file and function order - TestSoundNetActionIsNilUntilConfigured
// asserts the opposite state, and this file sorts before the one that holds it.
func withSoundNetAnalyser(t *testing.T, a *eventpipeline.Analyser) {
	t.Helper()
	previous := soundNetAnalyser
	soundNetAnalyser = a
	t.Cleanup(func() { soundNetAnalyser = previous })
}

// soundNetTestProcessor returns a processor configured the way the station is:
// SQLite on, so a DatabaseAction is built and the detection gets an ID.
func soundNetTestProcessor() *Processor {
	// The Output and SQLite sections are anonymous structs upstream, so they are
	// set field by field rather than built as a literal.
	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	return &Processor{
		Settings:     settings,
		EventTracker: NewEventTracker(0),
	}
}

// findSoundNetAction returns the SoundNet action and whether it was found inside
// a CompositeAction (rather than as a top-level sibling of one).
func findSoundNetAction(actions []Action) (found, inComposite bool, stepsBefore []string) {
	for _, a := range actions {
		if composite, ok := a.(*CompositeAction); ok {
			for _, inner := range composite.Actions {
				if _, isSoundNet := inner.(*SoundNetAction); isSoundNet {
					return true, true, stepsBefore
				}
				if inner != nil {
					stepsBefore = append(stepsBefore, inner.GetDescription())
				}
			}
			continue
		}
		if _, ok := a.(*SoundNetAction); ok {
			return true, false, stepsBefore
		}
	}
	return false, false, stepsBefore
}

// TestSoundNetActionRunsAfterTheDatabaseSave is the test that was missing, and
// its absence cost the project every diagnostics and enrichment row it should
// have written since M3 shipped.
//
// SoundNetAction reads the database-assigned detection ID from DetectionContext
// and returns immediately when it is still zero. DatabaseAction is what stores
// it. Every action getDefaultActions returns at the top level is enqueued as its
// own task on a shared worker queue and runs concurrently, so a top-level
// SoundNetAction races the database save - and loses it, because the save does
// disk I/O and the SoundNet action does not. The result is a permanent, silent
// skip: no row, no error, no log.
//
// Upstream already solved this for SSE and MQTT, which need the same ID, by
// putting them in a CompositeAction after the save. This asserts SoundNet is in
// that sequence too. The existing tests in actions_soundnet_test.go check that
// the action is *built* correctly, which it always was; this checks that it is
// *dispatched* somewhere it can work.
func TestSoundNetActionRunsAfterTheDatabaseSave(t *testing.T) {
	withSoundNetAnalyser(t, &eventpipeline.Analyser{Config: eventpipeline.DefaultConfig()})

	p := soundNetTestProcessor()
	det := testDetectionWithSpecies("Vehicle", "vehicle", 0.8)
	det.pcmData3s = make([]byte, 96000)
	det.Result.RawLabel = "vehicle"

	actions := p.getActionsForItem(&det)

	found, inComposite, before := findSoundNetAction(actions)
	require.True(t, found, "SoundNet is configured and the detection has a clip, so the action must be dispatched")
	assert.True(t, inComposite,
		"the SoundNet action must run inside the sequential composite, after the database save; "+
			"at the top level it is enqueued as an independent task and reads a detection ID of zero")
	assert.NotEmpty(t, before,
		"it must not be the first step either - something has to store the detection ID before it reads it")
}

// TestSoundNetActionIsLastInTheSequence keeps the ordering honest in the other
// direction. Diagnostics cost ~25 ms of DSP and enrichment makes an outbound
// HTTP call, and the composite gives each step its own CompositeActionTimeout;
// running last means a slow aircraft lookup cannot delay the SSE broadcast or
// the MQTT publish that a user is actually waiting on.
func TestSoundNetActionIsLastInTheSequence(t *testing.T) {
	withSoundNetAnalyser(t, &eventpipeline.Analyser{Config: eventpipeline.DefaultConfig()})

	p := soundNetTestProcessor()
	det := testDetectionWithSpecies("Vehicle", "vehicle", 0.8)
	det.pcmData3s = make([]byte, 96000)
	det.Result.RawLabel = "vehicle"

	for _, a := range p.getActionsForItem(&det) {
		composite, ok := a.(*CompositeAction)
		if !ok {
			continue
		}
		for i, inner := range composite.Actions {
			if _, isSoundNet := inner.(*SoundNetAction); isSoundNet {
				assert.Equal(t, len(composite.Actions)-1, i,
					"SoundNet must be the last step so its DSP and network call delay nothing downstream")
				return
			}
		}
	}
	t.Fatal("no SoundNet action inside the composite")
}
