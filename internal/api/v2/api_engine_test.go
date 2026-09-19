package api

import (
	"testing"

	"github.com/bert386/soundnet-go/internal/audiocore/engine"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEngineAtomicLoadStore(t *testing.T) {
	t.Parallel()
	e := echo.New()
	c := getTestController(t, e)

	// Initially nil.
	assert.Nil(t, c.Engine.Load())

	// Store and load back.
	eng := engine.New(t.Context(), &engine.Config{}, nil)
	defer eng.Stop()
	c.Engine.Store(eng)
	require.Same(t, eng, c.Engine.Load())
}

func TestWithAudioEngineOption(t *testing.T) {
	t.Parallel()
	e := echo.New()
	c := getTestController(t, e)

	eng := engine.New(t.Context(), &engine.Config{}, nil)
	defer eng.Stop()

	opt := WithAudioEngine(eng)
	opt(c)
	require.Same(t, eng, c.Engine.Load())
}
