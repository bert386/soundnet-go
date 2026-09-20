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
	//
	// A detection nothing resolved keeps its own domain and will not join an
	// aircraft pass. That is deliberate: a `Vehicle` row nobody identified may
	// genuinely be a car, and quietly folding it into the aeroplane beside it
	// would invent a fact from proximity alone.
	Domain string

	// Identity is what an authority named, empty when nothing did. An ICAO hex
	// code, a lightning strike id, whatever the provider is authoritative about.
	Identity string
}

// Pass is one source heard once.
type Pass struct {
	// ID is the first detection's ID, so a pass is named by something that
	// already exists and stays stable as later detections join it.
	ID uint

	// Identity is the one identity the pass's detections agree on, empty if
	// none of them was identified.
	Identity string

	Detections []Detection
	Start      time.Time
	End        time.Time
}

// Group sorts detections into passes.
//
// The rule is time proximity within a domain, split by identity: detections
// close together in one domain are the same pass unless an authority says they
// are two different things. Identity splits rather than joins, because it is
// the only signal that can contradict the timing - and when it does, it is
// right.
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

	byDomain := make(map[string][]Detection)
	for _, d := range detections {
		byDomain[d.Domain] = append(byDomain[d.Domain], d)
	}

	var passes []Pass
	for _, group := range byDomain {
		sort.SliceStable(group, func(i, j int) bool { return group[i].At.Before(group[j].At) })

		var run []Detection
		for _, d := range group {
			if len(run) > 0 && (d.At.Sub(run[len(run)-1].At) > gap || splitsOnIdentity(run, d)) {
				passes = append(passes, newPass(run))
				run = nil
			}
			run = append(run, d)
		}
		if len(run) > 0 {
			passes = append(passes, newPass(run))
		}
	}

	sort.SliceStable(passes, func(i, j int) bool { return passes[i].Start.Before(passes[j].Start) })
	return passes
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

func newPass(run []Detection) Pass {
	p := Pass{
		ID:         run[0].ID,
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
