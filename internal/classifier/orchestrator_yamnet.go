package classifier

import (
	"github.com/bert386/soundnet-go/internal/conf"
	"github.com/bert386/soundnet-go/internal/errors"
	"github.com/bert386/soundnet-go/internal/logger"
)

// SoundNet: the YAMNet loader.
//
// Follows the skeleton of orchestrator_perch_onnx.go - build, register, queue a
// path repair, defer warm-up - with two differences worth noting:
//
//   - YAMNet has no settings block of its own. Its files come from the model
//     gallery only, so path resolution has nothing user-configured to prefer and
//     no stale path to repair. A config section can be added later without
//     changing anything here.
//   - It is TFLite, not ONNX, so there is no runtime to probe, no OpenVINO path,
//     and no backend/device reload to handle. ReloadSecondaryModels rebuilds
//     ONNX-capable secondaries when the backend changes; YAMNet is not one, so
//     it is deliberately absent from openvinoCapableSecondaryBuilders.

// init registers the loader.
//
// modelLoaders is a package-level map initialised by a composite literal, and Go
// completes all package-level variable initialisation before running any init(),
// so extending it here is ordered correctly and keeps orchestrator.go untouched.
// Without this entry LoadModel("YAMNet") returns "loader not yet implemented"
// and loadEnabledModels logs a warning and skips - which is exactly what it did
// while the registration existed but the adapter did not.
func init() {
	modelLoaders[RegistryIDYAMNet] = (*Orchestrator).loadYAMNet
}

// buildYAMNet constructs a YAMNet instance from the given settings snapshot
// without registering it in o.models.
//
// The returned resolution is meaningful only when err == nil.
func (o *Orchestrator) buildYAMNet(settings *conf.Settings, threads int) (*YAMNet, pathResolution, error) {
	// Nothing configured to prefer: the empty modelFileSet means "whatever the
	// gallery installed", which is the only way YAMNet can be present.
	res := o.resolveFamilyPaths(RegistryIDYAMNet, modelFileSet{}, false)
	modelPath := res.resolved.model
	labelPath := res.resolved.labels

	if modelPath == "" || labelPath == "" {
		return nil, pathResolution{}, errors.Newf("YAMNet is not installed; install it from the model gallery").
			Component("classifier.orchestrator").
			Category(errors.CategoryModelInit).
			Context("model", RegistryIDYAMNet).
			Build()
	}

	yamnet, err := NewYAMNet(&YAMNetConfig{
		ModelPath: modelPath,
		LabelPath: labelPath,
		Threads:   threads,
		// Reuses the BirdNET setting rather than inventing a second one. XNNPACK
		// is a property of the host's CPU, not of the model, so a user who turned
		// it off did so for a reason that applies here too.
		UseXNNPACK: settings.BirdNET.UseXNNPACK,
	})
	if err != nil {
		return nil, pathResolution{}, errors.New(err).
			Component("classifier.orchestrator").
			Category(errors.CategoryModelInit).
			Context("model", RegistryIDYAMNet).
			Build()
	}

	return yamnet, res, nil
}

// loadYAMNet creates and registers a YAMNet instance.
// o.mu.Lock() is held by the caller.
func (o *Orchestrator) loadYAMNet(threads int) error {
	settings := o.currentSettings()
	before := o.captureRSSBefore()

	yamnet, res, err := o.buildYAMNet(settings, threads)
	if err != nil {
		return err
	}

	o.models[yamnet.ModelID()] = &modelEntry{
		instance: yamnet,
		backend:  secondaryTripletFor(settings),
	}
	o.queuePathCorrection(RegistryIDYAMNet, res)
	o.deferWarmup(yamnet.ModelID(), before)

	// No label resolver is registered. Upstream's resolvers map scientific names
	// to common names for species; AudioSet classes are neither, and running them
	// through a species resolver would either fail to match or, worse, match a
	// genus that happens to share a word.
	GetLogger().Info("YAMNet model loaded into Orchestrator",
		logger.String("model_id", yamnet.ModelID()),
		logger.Int("classes", yamnet.NumSpecies()))

	return nil
}
