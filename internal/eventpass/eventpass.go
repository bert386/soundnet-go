// Package eventpass groups the detections a single passing source produces.
//
// One aircraft crossing the sky does not make one detection. It makes several,
// spread over the half-minute or so it is audible and scattered across whatever
// classes each model reached for in each three-second window. A real morning at
// the deployment station:
//
//	06:39:56  Vehicle                0.74
//	06:39:57  Fixed-wing aircraft    0.26
//	06:40:10  Fixed-wing aircraft    0.23
//	06:40:16  Vehicle                0.34
//	06:40:46  Fixed-wing aircraft    0.20
//	06:40:48  Vehicle                0.47
//
// Six rows, one aeroplane - hex 8a047a, which ADS-B named for four of them.
// Thirteen such groups that morning and **every one named exactly one
// aircraft**; none named two. So the grouping is not a guess about timing that
// happens to work, it is a fact the transponder confirms.
//
// What this package does NOT do is discard anything. Each row is a model's
// opinion about a window, and those opinions are the training corpus and the
// raw material for corrections. Grouping is a reading of the detections, not a
// replacement for them.
package eventpass

import (
	"slices"
	"sort"
	"time"
)

// DefaultGap is how long a pass may go unheard before the next detection is
// treated as a new one.
//
// Thirty seconds, from the station's own data: the groups it produces span 38
// to 76 seconds with internal silences of up to 28, and it separated thirteen
// passes without ever merging two aircraft. Shorter would split a single
// aeroplane at its quietest moment, which is exactly the middle of the pass
// where it is directly overhead and the microphone is least sure.
const DefaultGap = 30 * time.Second

// Detection is the minimum a grouping needs to know. It is deliberately not the
// API or datastore type: this package decides how detections relate, and giving
// it a full record would let it start depending on fields that have nothing to
// do with the question.
type Detection struct {
	ID uint
	At time.Time

	// Domain is the event family, and callers must pass the **resolved** domain
	// where there is one, falling back to the class's own.
	//
	// This is the crux of the whole grouping and it is easy to get backwards.
	// `Vehicle`, `Thunderstorm` and `Fixed-wing aircraft` belong to three
	// different domains by their class names; grouping on those would scatter
	// one aeroplane into three passes, which is the problem rather than the
	// fix. They are one pass because ADS-B resolved all three to aircraft.
	Domain string

	// Identity is what an authority named, empty when nothing did. An ICAO hex
	// code, a lightning strike id, whatever the provider is authoritative about.
	Identity string

	// Candidates are the domains this detection's class could belong to - the
	// taxonomy's own ambiguity table, its own domain included. It is what lets
	// an unidentified `Thunderstorm` join the aeroplane it was recorded beside,
	// and what stops a `Purr` from doing the same.
	//
	// Empty means "settled", and a caller that does not supply it gets exactly
	// the grouping this package did before the field existed.
	Candidates []string
}

// Pass is one source heard once.
type Pass struct {
	// ID is the first detection's ID, so a pass is named by something that
	// already exists and stays stable as later detections join it.
	ID uint

	// Domain is the family the pass belongs to. Not simply the first
	// detection's: a pass that absorbed a Thunderstorm keeps the domain the
	// authority settled it under, and the absorbed row keeps its own class.
	Domain string

	// Identity is the one identity the pass's detections agree on, empty if
	// none of them was identified.
	Identity string

	Detections []Detection
	Start      time.Time
	End        time.Time
}

// Group sorts detections into passes.
//
// Two phases. First, time proximity within a domain, split by identity:
// detections close together in one domain are the same pass unless an authority
// says they are two different things. Identity splits rather than joins,
// because it is the only signal that can contradict the timing - and when it
// does, it is right.
//
// Then the passes an authority named absorb the unidentified detections lying
// inside them whose class the taxonomy already admits cannot tell the two
// domains apart. That second phase is what stops one flight appearing three
// times, once under aircraft, once under vehicle and once under weather.
//
// Detections need not arrive sorted. The returned passes are ordered by start
// time, and each pass's detections by their own.
func Group(detections []Detection, gap time.Duration) []Pass {
	if len(detections) == 0 {
		return nil
	}
	if gap <= 0 {
		gap = DefaultGap
	}

	passes := groupWithinDomains(detections, gap)
	passes = absorbAmbiguous(passes, gap)

	sort.SliceStable(passes, func(i, j int) bool { return passes[i].Start.Before(passes[j].Start) })
	return passes
}

// groupWithinDomains is the original rule: proximity inside one domain.
func groupWithinDomains(detections []Detection, gap time.Duration) []Pass {
	byDomain := make(map[string][]Detection)
	for _, d := range detections {
		byDomain[d.Domain] = append(byDomain[d.Domain], d)
	}

	var passes []Pass
	for domain, group := range byDomain {
		sort.SliceStable(group, func(i, j int) bool { return group[i].At.Before(group[j].At) })

		var run []Detection
		for _, d := range group {
			if len(run) > 0 && (d.At.Sub(run[len(run)-1].At) > gap || splitsOnIdentity(run, d)) {
				passes = append(passes, newPass(run, domain))
				run = nil
			}
			run = append(run, d)
		}
		if len(run) > 0 {
			passes = append(passes, newPass(run, domain))
		}
	}
	return passes
}

// absorbAmbiguous folds unidentified detections into the identified pass they
// were heard inside.
//
// Three conditions, all required, and each one is doing work:
//
//   - The receiving pass was named by an authority. Proximity between two
//     things nobody identified is not evidence of anything; ADS-B saying an
//     aeroplane was overhead at that second is.
//   - The moving detection was not itself identified. A row an authority named
//     already knows what it is, and moving it would overrule the authority with
//     a guess about timing.
//   - The taxonomy lists the receiving pass's domain among the moving class's
//     candidates. `Thunder` and `Vehicle` are ambiguous with aircraft and this
//     is exactly the confusion the ambiguity table was written for - at this
//     station every one of fifty reviewed Thunder detections was an aeroplane.
//     `Purr` is not ambiguous with anything, so a cat heard during a flypast
//     stays a cat.
//
// Windows are measured against the pass as phase one left it and are not
// widened as detections join, so absorbing cannot chain outward from a pass
// into a sound half a minute past its far end.
func absorbAmbiguous(passes []Pass, gap time.Duration) []Pass {
	anchors := make([]int, 0, len(passes))
	for i := range passes {
		if passes[i].Identity != "" {
			anchors = append(anchors, i)
		}
	}
	if len(anchors) == 0 {
		return passes
	}

	// Windows captured before anything moves, for the reason above.
	windows := make(map[int][2]time.Time, len(anchors))
	for _, i := range anchors {
		windows[i] = [2]time.Time{passes[i].Start, passes[i].End}
	}

	moved := false
	for i := range passes {
		if passes[i].Identity != "" {
			// An identified pass is settled. It gives nothing away, and the
			// authority that named it is not overruled by a neighbour.
			continue
		}
		kept := make([]Detection, 0, len(passes[i].Detections))
		for _, d := range passes[i].Detections {
			target := nearestAnchor(passes, anchors, windows, d, gap)
			if target < 0 {
				kept = append(kept, d)
				continue
			}
			passes[target].Detections = append(passes[target].Detections, d)
			moved = true
		}
		passes[i].Detections = kept
	}
	if !moved {
		return passes
	}

	out := make([]Pass, 0, len(passes))
	for i := range passes {
		if len(passes[i].Detections) == 0 {
			// A pass that gave up every detection it had is not an empty pass,
			// it is one that turned out to be part of another.
			continue
		}
		sort.SliceStable(passes[i].Detections, func(a, b int) bool {
			return passes[i].Detections[a].At.Before(passes[i].Detections[b].At)
		})
		out = append(out, newPass(passes[i].Detections, passes[i].Domain))
	}
	return out
}

// nearestAnchor picks the identified pass this detection belongs to, or -1.
//
// Nearest in time rather than first found: two aircraft can pass within a
// minute of each other, and the ambiguous row between them belongs to whichever
// was closer to it, not to whichever the map happened to yield first.
func nearestAnchor(
	passes []Pass,
	anchors []int,
	windows map[int][2]time.Time,
	d Detection,
	gap time.Duration,
) int {
	if d.Identity != "" || len(d.Candidates) == 0 {
		return -1
	}

	best := -1
	var bestDistance time.Duration
	for _, i := range anchors {
		if !slices.Contains(d.Candidates, passes[i].Domain) {
			continue
		}
		window := windows[i]
		distance := distanceToWindow(window[0], window[1], d.At)
		if distance > gap {
			continue
		}
		if best < 0 || distance < bestDistance {
			best, bestDistance = i, distance
		}
	}
	return best
}

// distanceToWindow is how far outside [start, end] a moment falls, zero when it
// falls inside.
func distanceToWindow(start, end, at time.Time) time.Duration {
	switch {
	case at.Before(start):
		return start.Sub(at)
	case at.After(end):
		return at.Sub(end)
	default:
		return 0
	}
}

// splitsOnIdentity reports whether this detection names something different
// from what the run has already been told it is.
//
// Only a contradiction splits. An unidentified detection joins whatever it
// falls inside, because most detections in a pass are never identified - four
// of the six rows in the example above were not - and requiring an identity to
// join would scatter a pass back into the rows it was assembled from.
func splitsOnIdentity(run []Detection, next Detection) bool {
	if next.Identity == "" {
		return false
	}
	for _, d := range run {
		if d.Identity != "" && d.Identity != next.Identity {
			return true
		}
	}
	return false
}

func newPass(run []Detection, domain string) Pass {
	p := Pass{
		ID:         run[0].ID,
		Domain:     domain,
		Detections: run,
		Start:      run[0].At,
		End:        run[len(run)-1].At,
	}
	for _, d := range run {
		if d.Identity != "" {
			p.Identity = d.Identity
			break
		}
	}
	return p
}

// PassIDs maps each detection to the pass it belongs to, which is the form a
// list endpoint needs: it annotates rows rather than restructuring them, so a
// caller that does not care about passes is unaffected.
func PassIDs(detections []Detection, gap time.Duration) map[uint]uint {
	passes := Group(detections, gap)
	out := make(map[uint]uint, len(detections))
	for _, p := range passes {
		for _, d := range p.Detections {
			out[d.ID] = p.ID
		}
	}
	return out
}
