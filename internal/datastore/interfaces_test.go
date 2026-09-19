package datastore

import (
	"testing"

	"github.com/bert386/soundnet-go/internal/conf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createDatabase initializes a temporary database for testing purposes.
// It ensures the database connection is opened and handles potential errors.
// Close() calls StopMonitoring() which blocks until all background goroutines
// (integrity check, connection pool monitoring) have exited.
func createDatabase(t *testing.T, settings *conf.Settings) Interface {
	t.Helper()
	tempDir := t.TempDir()
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = tempDir + "/test.db"

	dataStore := New(settings)

	require.NoError(t, dataStore.Open(), "Failed to open database")

	t.Cleanup(func() {
		assert.NoError(t, dataStore.Close(), "Failed to close datastore")
	})

	return dataStore
}
