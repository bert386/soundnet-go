package classifier

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SoundNet: the model gallery was built around HuggingFace, and YAMNet is not
// hosted there. CatalogEntry.BaseURL is the one-field escape hatch that lets an
// entry name its own host; these tests pin its behaviour, because the failure
// it prevents is silent - a gallery install that 404s against a HuggingFace repo
// that never existed.

// yamnetEntry returns the YAMNet entry as the gallery sees it. Registered from
// an init() in model_yamnet.go, so it is present in EmbeddedCatalog without any
// upstream file declaring it.
func yamnetEntry(t *testing.T) CatalogEntry {
	t.Helper()
	entry, ok := GetCatalogEntry("yamnet-v1")
	require.True(t, ok, "YAMNet must be registered in the embedded catalog")
	return entry
}

func TestYAMNetEntryNamesItsOwnHost(t *testing.T) {
	t.Parallel()
	entry := yamnetEntry(t)

	assert.Equal(t, SoundNetModelBaseURL, entry.BaseURL,
		"without BaseURL the gallery resolves the entry against HuggingFace, where these files do not exist")
	assert.Equal(t, CategoryAcousticEvent, entry.Category)
	assert.Equal(t, RegistryIDYAMNet, entry.RegistryID)
}

func TestYAMNetFilesAreAllPinned(t *testing.T) {
	t.Parallel()
	entry := yamnetEntry(t)
	require.Len(t, entry.Files, 2, "the model and its class map")

	for _, f := range entry.Files {
		// The class map matters as much as the model: it is the join between
		// YAMNet's output indices and the event taxonomy, so a row inserted
		// anywhere in it relabels every class below without anything failing.
		assert.NotEmpty(t, f.SHA256, "file %q must be pinned", f.RemotePath)
		assert.Positive(t, f.SizeBytes, "file %q must declare its size for the disk preflight", f.RemotePath)
	}
}

// TestOnlySoundNetEntriesSetBaseURL keeps the field fork-local. If an upstream
// entry ever sets it, a merge has moved a model off HuggingFace and the mirror
// assumptions here need re-reading rather than silently applying.
func TestOnlySoundNetEntriesSetBaseURL(t *testing.T) {
	t.Parallel()
	for i := range EmbeddedCatalog {
		e := &EmbeddedCatalog[i]
		if e.BaseURL == "" {
			continue
		}
		// SOUNDNET: both of the fork's acoustic-event models are mirrored.
		assert.Contains(t, []string{"yamnet-v1", "ced-tiny-v1"}, e.ID,
			"entry %q sets BaseURL; only SoundNet's own entries should", e.ID)
	}
}

// TestClassMapFixtureIsTheShippedArtefact ties internal/eventclass's test
// fixture to the file the installer actually fetches.
//
// The taxonomy validates its 66 AudioSet indices against that fixture, which is
// only worth anything if the fixture is the same bytes YAMNet will be run with.
// They had already drifted once - the fixture was committed with CRLF line
// endings, so it carried the right content under a different checksum - and
// nothing anywhere would have noticed if the content had drifted too.
//
// Reaching across package directories in a test is deliberate: the two files
// have to be identical, and the assertion belongs where the pin lives.
func TestClassMapFixtureIsTheShippedArtefact(t *testing.T) {
	t.Parallel()
	entry := yamnetEntry(t)

	var pinned string
	for _, f := range entry.Files {
		if f.LocalName == "yamnet_class_map.csv" {
			pinned = f.SHA256
		}
	}
	require.NotEmpty(t, pinned, "the catalog must pin the class map")

	fixture, err := os.ReadFile(filepath.Join("..", "eventclass", "testdata", "yamnet_class_map.csv"))
	require.NoError(t, err, "the eventclass fixture must exist")

	assert.Equal(t, pinned, fmt.Sprintf("%x", sha256.Sum256(fixture)),
		"the class map the taxonomy is tested against must be byte-identical to the one that ships; "+
			"if this fails, one of the two was regenerated and the indices may no longer mean what the taxonomy says")
}

// baseURLServerEntry builds a flat entry whose files live at an httptest server
// named by the entry itself rather than passed in by the caller - the shape a
// real gallery install takes.
func baseURLServerEntry(t *testing.T) (entry CatalogEntry, modelsDir string, hits *[]string) {
	t.Helper()
	model := []byte("mirrored-model-bytes")
	labels := []byte("index,mid,display_name\n0,/m/09x0r,Speech\n")

	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		switch r.URL.Path {
		case "/mirror/model.tflite":
			_, _ = w.Write(model)
		case "/mirror/class_map.csv":
			_, _ = w.Write(labels)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	entry = CatalogEntry{
		ID:      "test-baseurl",
		Name:    "Mirror-hosted Model",
		Version: "1.0",
		// Deliberately points at a repo that does not exist: the install must
		// never reach HuggingFace, and naming a real repo would hide it if it did.
		HuggingFaceRepo: "does-not-exist/nowhere",
		BaseURL:         srv.URL + "/mirror",
		Files: []CatalogFile{
			{RemotePath: "model.tflite", LocalName: "model.tflite", Role: RoleModel, SHA256: sha256Hex(model), SizeBytes: int64(len(model))},
			{RemotePath: "class_map.csv", LocalName: "class_map.csv", Role: RoleLabels, SHA256: sha256Hex(labels), SizeBytes: int64(len(labels))},
		},
	}
	return entry, t.TempDir(), &seen
}

func TestInstallFetchesFromEntryBaseURL(t *testing.T) {
	t.Parallel()
	entry, modelsDir, hits := baseURLServerEntry(t)
	mm := NewModelManager(modelsDir, nil, nil)

	// No caller-supplied baseURL: this is the path the gallery's install handler
	// takes, and the path that was broken before BaseURL existed.
	require.NoError(t, mm.Install(t.Context(), &entry, "", "", nil))

	assert.ElementsMatch(t, []string{"/mirror/model.tflite", "/mirror/class_map.csv"}, *hits,
		"both files must come from the entry's own host")
	for _, name := range []string{"model.tflite", "class_map.csv"} {
		_, err := os.Stat(filepath.Join(modelsDir, entry.ID, name))
		require.NoError(t, err, "%s should be on disk after install", name)
	}
	assert.True(t, mm.IsInstalled(entry.ID))
}

func TestCallerBaseURLStillWinsOverTheEntry(t *testing.T) {
	t.Parallel()
	entry, modelsDir, entryHits := baseURLServerEntry(t)

	model := []byte("injected-model-bytes")
	labels := []byte("injected-labels\n")
	var overrideHits []string
	override := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		overrideHits = append(overrideHits, r.URL.Path)
		switch r.URL.Path {
		case "/model.tflite":
			_, _ = w.Write(model)
		case "/class_map.csv":
			_, _ = w.Write(labels)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(override.Close)

	entry.Files[0].SHA256, entry.Files[0].SizeBytes = sha256Hex(model), int64(len(model))
	entry.Files[1].SHA256, entry.Files[1].SizeBytes = sha256Hex(labels), int64(len(labels))

	mm := NewModelManager(modelsDir, nil, nil)
	require.NoError(t, mm.Install(t.Context(), &entry, "", override.URL, nil))

	// Test injection has to keep working, or every existing download test would
	// start pulling from whatever host the entry happens to name.
	assert.ElementsMatch(t, []string{"/model.tflite", "/class_map.csv"}, overrideHits)
	assert.Empty(t, *entryHits, "the entry's own host must not be contacted when the caller names one")
}
