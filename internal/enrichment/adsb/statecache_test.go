package adsb_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bert386/soundnet-go/internal/enrichment/adsb"
)

// stateServer counts how many /states/all requests were actually billed.
func stateServer(t *testing.T, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			_, _ = w.Write([]byte(`{"access_token":"t","expires_in":1800}`))
			return
		}
		calls.Add(1)
		_, _ = w.Write([]byte(`{"time":1,"states":[]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func cachingClient(srv *httptest.Server, now func() time.Time) *adsb.OpenSkyClient {
	c := adsb.NewOpenSkyClient("id", "secret")
	c.BaseURL, c.TokenURL = srv.URL, srv.URL+"/token"
	c.SetClock(now)
	return c
}

// TestSkyIsReusedWithinTheTTL is the cost control that makes asking about
// ambiguous vehicle labels affordable. /states/all has no time parameter - it
// returns the current sky and costs a credit each time - so two detections
// seconds apart were being billed twice for identical data.
func TestSkyIsReusedWithinTheTTL(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := stateServer(t, &calls)

	base := time.Date(2026, 9, 20, 10, 51, 0, 0, time.UTC)
	at := base
	c := cachingClient(srv, func() time.Time { return at })

	_, err := c.StatesInBox(t.Context(), -35, 150, -33, 152)
	require.NoError(t, err)

	at = base.Add(2 * time.Second)
	_, err = c.StatesInBox(t.Context(), -35, 150, -33, 152)
	require.NoError(t, err)

	assert.EqualValues(t, 1, calls.Load(), "the sky did not change, so it must not be bought twice")
}

// TestSkyIsRefetchedAfterTheTTL is the other half. The window is short because
// the lag correction back-projects an aircraft's track from its reported
// position, and past a few seconds that projection stops being trustworthy - a
// stale sky would produce a confident wrong match, which is worse than none.
func TestSkyIsRefetchedAfterTheTTL(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := stateServer(t, &calls)

	base := time.Date(2026, 9, 20, 10, 51, 0, 0, time.UTC)
	at := base
	c := cachingClient(srv, func() time.Time { return at })

	_, err := c.StatesInBox(t.Context(), -35, 150, -33, 152)
	require.NoError(t, err)

	at = base.Add(adsb.DefaultStateTTL + time.Millisecond)
	_, err = c.StatesInBox(t.Context(), -35, 150, -33, 152)
	require.NoError(t, err)

	assert.EqualValues(t, 2, calls.Load())
}

// TestADifferentBoxIsNotServedFromCache guards the obvious way a cache like this
// goes wrong: returning one station's sky for another's query.
func TestADifferentBoxIsNotServedFromCache(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := stateServer(t, &calls)

	at := time.Date(2026, 9, 20, 10, 51, 0, 0, time.UTC)
	c := cachingClient(srv, func() time.Time { return at })

	_, err := c.StatesInBox(t.Context(), -35, 150, -33, 152)
	require.NoError(t, err)
	_, err = c.StatesInBox(t.Context(), -20, 140, -18, 142)
	require.NoError(t, err)

	assert.EqualValues(t, 2, calls.Load())
}

func TestReuseCanBeDisabled(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := stateServer(t, &calls)

	at := time.Date(2026, 9, 20, 10, 51, 0, 0, time.UTC)
	c := cachingClient(srv, func() time.Time { return at })
	c.StateTTL = -1

	for range 3 {
		_, err := c.StatesInBox(t.Context(), -35, 150, -33, 152)
		require.NoError(t, err)
	}
	assert.EqualValues(t, 3, calls.Load())
}
