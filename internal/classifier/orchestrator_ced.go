package classifier

// SOUNDNET: the CED loader.
//
// Follows orchestrator_yamnet.go, with one difference that matters: CED is ONNX,
// so it needs the ONNX Runtime shared library. That is a real prerequisite on a
// Raspberry Pi rather than a formality - the station ran for weeks with only
// libtensorflowlite_c.so present, which silently meant no ONNX model in the
// catalog could load at all.
//
// The runtime path comes from BirdNET's settings rather than a new one of our
// own: it is a property of the host, not of any model, and a user who pointed
// BirdNET v3 at a library meant it for every ONNX model.

import (
	"github.com/bert386/soundnet-go/internal/conf"
	"github.com/bert386/soundnet-go/internal/errors"
	"github.com/bert386/soundnet-go/internal/logger"
)

// init registers the loader. See orchestrator_yamnet.go for why extending
// modelLoaders from an init is correctly ordered.
func init() {
	modelLoaders[RegistryIDCED] = (*Orchestrator).loadCED
}

// buildCED constructs a CED instance from the given settings snapshot without
// registering it in o.models.
func (o *Orchestrator) buildCED(settings *conf.Settings, threads int) (*CED, pathResolution, error) {
	// Nothing configured to prefer: like YAMNet, CED has no settings block of
	// its own and its files come from the model gallery only.
	res := o.resolveFamilyPaths(RegistryIDCED, modelFileSet{}, false)
	modelPath := res.resolved.model
	labelPath := res.resolved.labels

	if modelPath == "" || labelPath == "" {
		return nil, pathResolution{}, errors.Newf("CED is not installed; install it from the model gallery").
			Component("classifier.orchestrator").
			Category(errors.CategoryModelInit).
			Context("model", RegistryIDCED).
			Build()
	}

	ced, err := NewCED(&CEDConfig{
		ModelPath: modelPath,
		LabelPath: labelPath,
		Threads:   threads,
		// BirdNET's setting, deliberately. The ONNX Runtime is a property of the
		// host; a second key for the same library would be one more thing to get
		// out of step.
		ONNXRuntimePath: settings.BirdNET.ONNXRuntimePath,
	})
	if err != nil {
		return nil, pathResolution{}, err
	}
	return ced, res, nil
}

// loadCED creates and registers a CED instance.
// o.mu.Lock() is held by the caller.
func (o *Orchestrator) loadCED(threads int) error {
	settings := o.currentSettings()
	before := o.captureRSSBefore()

	ced, res, err := o.buildCED(settings, threads)
	if err != nil {
		return err
	}

	o.models[ced.ModelID()] = &modelEntry{
		instance: ced,
		backend:  secondaryTripletFor(settings),
	}
	o.queuePathCorrection(RegistryIDCED, res)
	o.deferWarmup(ced.ModelID(), before)

	// No label resolver, for YAMNet's reason: AudioSet classes are not species,
	// and a species resolver would either fail to match or match a genus that
	// happens to share a word.
	GetLogger().Info("CED model loaded into Orchestrator",
		logger.String("model_id", ced.ModelID()),
		logger.Int("classes", ced.NumSpecies()))

	return nil
}
