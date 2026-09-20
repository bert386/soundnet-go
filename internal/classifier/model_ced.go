package classifier

import "time"

// SOUNDNET: CED-tiny registration.
//
// Added to the registry and the gallery catalog by init(), like YAMNet, so the
// fork's footprint in this package stays new files and no modified lines.
//
// CED sits alongside YAMNet rather than replacing it. They disagree usefully:
// on the operator-labelled set YAMNet reaches higher absolute scores on the
// aircraft it does find, while CED is far better at not confusing a lorry with
// an aeroplane or a whistle with a siren. Running both and letting the
// cross-model consensus machinery see them is worth more than picking one,
// and costs about 200 ms a window on a Pi 4 that currently spends 240.

const (
	// RegistryIDCED identifies CED in the model registry.
	RegistryIDCED = "CED"

	// cedClasses is the full AudioSet ontology. YAMNet drops six of these and
	// renumbers, which is why the taxonomy's AudioSetIndex column - pinned to
	// YAMNet - must not be used to interpret CED's output. See ced.go.
	cedClasses = 527

	// cedSampleRate and cedClipSeconds are fixed by the fused export. The graph
	// carries its own mel front-end at 16 kHz, and CED interpolates positional
	// embeddings from the input length, so the traced window length is part of
	// the model rather than a parameter.
	cedSampleRate  = 16000
	cedClipSeconds = 3

	// cedWindowSamples is exactly what one inference consumes. Stated because a
	// wrongly sized window is rejected by the graph rather than silently
	// mishandled, and the error is much easier to read next to this number.
	cedWindowSamples = cedSampleRate * cedClipSeconds

	// cedScoreFloor drops classes that scored essentially nothing before the
	// top-K sort. Not a detection threshold - that is the user's, applied
	// downstream - only a guard against returning 500-odd rows of noise.
	cedScoreFloor = 0.01
)

// The artefact is pinned for the same reason YAMNet's is, and a stronger one:
// this is a re-export rather than a published file, so nothing else in the
// world can vouch for it. A silently different model would still load and still
// produce 527 scores, and every detection would be quietly mislabelled.
//
// Produced by doc/soundnet/eval/export_ced_fused.py from RicherMans/CED's
// published ced_tiny weights, and verified against PyTorch to 1.3e-06 across
// twelve real windows - see doc/soundnet/MODEL_EVAL.md.
const (
	cedModelSHA256 = "5116eb899a3f73e223ed0c8a5017119e6ee65697a9af5216a79a8d673ad5a9af"
	cedModelBytes  = 22789951

	// The class map is the join between CED's output indices and the event
	// taxonomy, matched on label text. Pinned because a row inserted anywhere
	// shifts every label below it and nothing would fail.
	cedClassMapSHA256 = "cdd1049833c4b86127c2773ac0d14a2754b6a6d0d1798002ed5c66e699708429"
	cedClassMapBytes  = 14675
)

func init() {
	ModelRegistry[RegistryIDCED] = ModelInfo{
		ID:               RegistryIDCED,
		Name:             RegistryIDCED,
		Backend:          BackendONNX,
		DetectionName:    RegistryIDCED,
		DetectionVersion: "1",
		Description:      "AudioSet acoustic event classifier, 527 classes, distilled from transformer ensembles",
		Spec: ModelSpec{
			SampleRate: cedSampleRate,
			ClipLength: cedClipSeconds * time.Second,
		},
		ConfigAliases: []string{"ced", "ced-tiny", "ced_tiny"},
		// No locale list, for YAMNet's reason: these are AudioSet display names,
		// not species names, and offering locales would imply a translation that
		// does not exist.
		DefaultLocale: "en",
		NumSpecies:    cedClasses,
		Quantization:  QuantizationFP32,
	}

	EmbeddedCatalog = append(EmbeddedCatalog, CatalogEntry{
		ID:            "ced-tiny-v1",
		Name:          "CED-tiny",
		Description:   "Consistent Ensemble Distillation audio tagger, 527 AudioSet classes. Separates road vehicles from aircraft where YAMNet cannot, and is markedly less prone to confident nonsense at low signal-to-noise. Mel front-end fused into the graph.",
		Author:        "RicherMans",
		License:       "Apache-2.0",
		CommercialUse: true,
		Category:      CategoryAcousticEvent,
		SpeciesCount:  cedClasses,
		Version:       "1",
		RegistryID:    RegistryIDCED,
		UpstreamURL:   "https://github.com/RicherMans/CED",
		// Requires ONNX Runtime, which YAMNet does not. A station with only the
		// TFLite backend will see this entry and be unable to install it, which
		// is the honest outcome - the alternative is hiding a model that works
		// the moment the runtime is present.
		HuggingFaceRepo: soundNetModelRepo,
		BaseURL:         SoundNetModelBaseURL,
		Files: []CatalogFile{
			{
				RemotePath: "ced/ced_tiny_fused.onnx",
				LocalName:  "ced_tiny_fused.onnx",
				Role:       RoleModel,
				SHA256:     cedModelSHA256,
				SizeBytes:  cedModelBytes,
			},
			{
				RemotePath: "ced/class_labels_indices.csv",
				LocalName:  "class_labels_indices.csv",
				Role:       RoleLabels,
				SHA256:     cedClassMapSHA256,
				SizeBytes:  cedClassMapBytes,
			},
		},
	})
}

// CEDSpec returns CED's audio requirements without reaching into the registry.
func CEDSpec() ModelSpec {
	return ModelSpec{SampleRate: cedSampleRate, ClipLength: cedClipSeconds * time.Second}
}

// CEDWindowSamples returns how many samples one CED inference consumes.
func CEDWindowSamples() int { return cedWindowSamples }
