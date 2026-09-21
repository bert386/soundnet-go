package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTaxonomyReportsDomainAmbiguity checks the wiring rather than the taxonomy.
//
// Two of this project's bugs were features that were correct in isolation and
// never reached: a category filter that passed every unit test while being a
// no-op in production, and a domain that resolved to "other" with nothing
// logged. So this asserts the ambiguity is visible on the response an operator
// receives, not merely computable.
func TestTaxonomyReportsDomainAmbiguity(t *testing.T) {
	t.Parallel()

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/soundnet/taxonomy", http.NoBody)
	rec := httptest.NewRecorder()

	require.NoError(t, (&Controller{}).GetSoundNetTaxonomy(e.NewContext(req, rec)))
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Domains []struct {
			Domain  string `json:"domain"`
			Classes []struct {
				Label            string   `json:"label"`
				CandidateDomains []string `json:"candidateDomains"`
			} `json:"classes"`
		} `json:"domains"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	candidates := map[string][]string{}
	for _, d := range body.Domains {
		for _, c := range d.Classes {
			candidates[c.Label] = c.CandidateDomains
		}
	}

	require.Contains(t, candidates, "Vehicle")
	assert.Equal(t, []string{"vehicle", "aircraft", "rail", "watercraft", "music"}, candidates["Vehicle"],
		"the superclass an overflight actually scores highest on must offer the aircraft reading")
	assert.Equal(t, []string{"vehicle", "aircraft", "rail", "watercraft", "music"}, candidates["Engine"])

	// The field is omitted where the domain is settled, so its presence means
	// something rather than decorating every row.
	require.Contains(t, candidates, "Car")
	assert.Empty(t, candidates["Car"], "a car is a car; offering it to an aircraft authority costs a credit for nothing")
	assert.Empty(t, candidates["Helicopter"])
}
