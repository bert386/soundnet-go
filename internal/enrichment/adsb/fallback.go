package adsb

// Chaining state sources, so running out of one is not running out of sky.

import (
	"context"
	"errors"
	"fmt"
)

// NamedSource is a StateSource that can say what it is, for logging. Sources
// that cannot are still usable; they are just reported by position.
type NamedSource interface {
	StateSource
	Name() string
}

// FallbackSource tries each source in turn until one answers.
//
// The order is the policy: a metered source with a documented limit is a better
// thing to depend on than an unmetered one with an undocumented limit, so
// OpenSky stays first and adsb.lol catches what falls through. The point is not
// to spread load evenly - it is that the station should never again spend a
// morning unable to identify anything because one counter reached zero.
type FallbackSource struct {
	Sources []StateSource

	// OnFallback, if set, is called when a source is passed over, with its
	// position in the chain and why. Without it the chain is silent, and a
	// primary that has been failing for a week looks exactly like one that is
	// working - the failure this project has now made twice.
	OnFallback func(source string, err error)
}

// CreditsRemaining reports the balance of the first source that meters itself,
// and reports nothing once a source behind it does not.
//
// The subtlety worth stating: callers use this to decide when to stop spending.
// With an unmetered source in the chain there is nothing to stop for, so
// claiming a ceiling would idle a collector that could have kept working. A
// chain is only as limited as its last resort.
func (f *FallbackSource) CreditsRemaining() (credits int, known bool) {
	for _, source := range f.Sources {
		reporter, ok := source.(interface{ CreditsRemaining() (int, bool) })
		if !ok {
			return 0, false
		}
		c, known := reporter.CreditsRemaining()
		if !known {
			return 0, false
		}
		if c > 0 {
			return c, true
		}
	}
	// Every source metered itself and every one is spent.
	return 0, true
}

// StatesInBox asks each source in order.
//
// Anything that is not a definite answer moves to the next source: a withheld
// request, a rate limit, a network failure, a malformed document. The
// alternative - falling through only on the credit sentinel - would leave the
// station blind whenever the primary was merely broken rather than exhausted,
// which is the more common way for a service to fail.
func (f *FallbackSource) StatesInBox(ctx context.Context, latMin, lonMin, latMax, lonMax float64) ([]State, error) {
	if len(f.Sources) == 0 {
		return nil, errors.New("adsb: no state sources configured")
	}

	var errs []error
	for i, source := range f.Sources {
		states, err := source.StatesInBox(ctx, latMin, lonMin, latMax, lonMax)
		if err == nil {
			return states, nil
		}
		// A cancelled context is the caller giving up, not this source failing.
		// Trying the next one would spend a second request on a question nobody
		// is waiting for the answer to.
		if ctx.Err() != nil {
			return nil, err
		}
		errs = append(errs, fmt.Errorf("%s: %w", sourceName(source, i), err))
		if f.OnFallback != nil {
			f.OnFallback(sourceName(source, i), err)
		}
	}
	return nil, fmt.Errorf("adsb: every state source failed: %w", errors.Join(errs...))
}

func sourceName(source StateSource, index int) string {
	if named, ok := source.(NamedSource); ok {
		return named.Name()
	}
	return fmt.Sprintf("source %d", index+1)
}
