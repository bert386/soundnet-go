package classifier

import "time"

// SoundNet: YAMNet registration.
//
// This file adds YAMNet to the model registry and the gallery catalog without
// editing either of the upstream files that hold them. Both are package-level
// variables, so an init here can extend them - which keeps the fork's footprint
// in this package to one added file and no modified lines.
//
// YAMNet differs from every model upstream ships, and that is the point: it is
// an acoustic event classifier over the AudioSet ontology rather than a species
// classifier. It is what turns this from a bird detector into a general event
// detector.

const (
	// RegistryIDYAMNet identifies YAMNet in the model registry.
	RegistryIDYAMNet = "YAMNet"

	// CategoryAcousticEvent is a new gallery category. Upstream's categories are
	// all taxonomic - wildlife, bird, bat, geomodel - and YAMNet belongs to none
	// of them, so grouping it under one of those would misdescribe it in the UI.
	CategoryAcousticEvent = "acoustic-event"

	// yamnetClasses is the size of YAMNet's output vector: the AudioSet ontology.
	yamnetClasses = 521

	// yamnetEmbeddingDim is the width of YAMNet's penultimate layer. Recorded
	// because the M4 sub-classification heads are trained on these embeddings
	// rather than on raw audio, which is what makes them cheap enough to run on
	// a Raspberry Pi alongside everything else.
	yamnetEmbeddingDim = 1024

	// yamnetSampleRate and yamnetClipLength are fixed by the model. YAMNet
	// consumes 0.975 s of 16 kHz mono - 15600 samples - which is a different
	// framing from every other model in the registry. The pipeline already
	// supports per-model framing (BirdNET runs 48 kHz/3 s, the bat models
	// 256 kHz), so this needs no pipeline change.
	yamnetSampleRate = 16000
	yamnetClipLength = 975 * time.Millisecond
)

// yamnetModelSHA256 pins the exact artefact.
//
// Taken from Google's MediaPipe-hosted build of YAMNet, verified as a valid
// TFLite flatbuffer (TFL3 magic) at 4,126,810 bytes. Pinning matters more than
// usual here: a silently different model would still load and still produce 521
// scores, but the class indices the event taxonomy maps against could differ,
// and every detection would be quietly mislabelled.
const (
	yamnetModelSHA256 = "4d8b4a53282dc83ef04e3e7dbc4fbc98082e34e44ed798e16c3a0cdd4c584faf"
	yamnetModelBytes  = 4126810
)

// soundNetModelRepo is the HuggingFace repository mirroring the model files.
//
// A mirror rather than Google's URL directly, for two reasons: upstream's
// gallery fetches from HuggingFace and teaching it arbitrary URLs would mean
// editing the download path, and MediaPipe's paths have moved before, so a
// pinned mirror does not rot. YAMNet is Apache 2.0, which permits redistribution.
const soundNetModelRepo = "bert386/soundnet-models"

func init() {
	ModelRegistry[RegistryIDYAMNet] = ModelInfo{
		ID:               RegistryIDYAMNet,
		Name:             RegistryIDYAMNet,
		Backend:          BackendTFLite,
		DetectionName:    RegistryIDYAMNet,
		DetectionVersion: "1",
		Description:      "AudioSet acoustic event classifier (521 classes: aircraft, vehicles, gunshots, thunder, tools and more)",
		Spec: ModelSpec{
			SampleRate: yamnetSampleRate,
			ClipLength: yamnetClipLength,
		},
		ConfigAliases: []string{"yamnet", "yamnet-v1"},
		// Deliberately no locale list. YAMNet's labels are AudioSet display
		// names, not species names, so upstream's species-name translation
		// machinery does not apply to them; presenting locales would imply a
		// translation that does not exist.
		DefaultLocale: "en",
		NumSpecies:    yamnetClasses,
		Quantization:  QuantizationFP32,
	}

	EmbeddedCatalog = append(EmbeddedCatalog, CatalogEntry{
		ID:              "yamnet-v1",
		Name:            "YAMNet",
		Description:     "Google's AudioSet acoustic event classifier. Detects 521 sound classes including aircraft, road vehicles, gunshots, thunder and power tools. The basis of SoundNet's general event detection.",
		Author:          "Google Research",
		License:         "Apache-2.0",
		CommercialUse:   true,
		Category:        CategoryAcousticEvent,
		SpeciesCount:    yamnetClasses,
		Version:         "1",
		RegistryID:      RegistryIDYAMNet,
		UpstreamURL:     "https://github.com/tensorflow/models/tree/master/research/audioset/yamnet",
		HuggingFaceRepo: soundNetModelRepo,
		Files: []CatalogFile{
			{
				RemotePath: "yamnet/yamnet.tflite",
				LocalName:  "yamnet.tflite",
				Role:       RoleModel,
				SHA256:     yamnetModelSHA256,
				SizeBytes:  yamnetModelBytes,
			},
			{
				// The class map is the join key between YAMNet's output indices
				// and the event taxonomy. It ships with the model rather than
				// being embedded, so a future model revision brings its own.
				RemotePath: "yamnet/yamnet_class_map.csv",
				LocalName:  "yamnet_class_map.csv",
				Role:       RoleLabels,
			},
		},
	})
}

// YAMNetSpec returns YAMNet's audio requirements, for callers that need the
// framing without reaching into the registry.
func YAMNetSpec() ModelSpec {
	return ModelSpec{SampleRate: yamnetSampleRate, ClipLength: yamnetClipLength}
}

// YAMNetEmbeddingDim returns the embedding width the M4 heads consume.
func YAMNetEmbeddingDim() int { return yamnetEmbeddingDim }

// YAMNetFrameSamples returns how many samples one YAMNet inference consumes.
//
// 15600 at 16 kHz. Stated as a function rather than left implicit because it is
// a number the framing code has to get exactly right: YAMNet silently produces
// nonsense from a wrongly sized window rather than refusing it.
func YAMNetFrameSamples() int {
	return int(float64(yamnetSampleRate) * yamnetClipLength.Seconds())
}
