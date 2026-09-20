package eventpass_test

import (
	"testing"
	"time"

	"github.com/bert386/soundnet-go/internal/eventpass"
)

var base = time.Date(2026, 9, 21, 6, 39, 56, 0, time.Local)

func at(seconds int) time.Time { return base.Add(time.Duration(seconds) * time.Second) }

func ids(p *eventpass.Pass) []uint {
	out := make([]uint, len(p.Detections))
	for i, d := range p.Detections {
		out[i] = d.ID
	}
	return out
}

func equal(got, want []uint) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// The real morning that prompted this: six rows, one aeroplane, three class
// names between them. All resolved to aircraft, so all one pass.
func TestGroupJoinsOneFlybyAcrossItsClassNames(t *testing.T) {
	t.Parallel()

	in := []eventpass.Detection{
		{ID: 1866, At: at(0), Domain: "aircraft", Identity: "8a047a"},  // Vehicle 0.74, resolved
		{ID: 1867, At: at(1), Domain: "aircraft", Identity: "8a047a"},  // Fixed-wing
		{ID: 1868, At: at(14), Domain: "aircraft", Identity: "8a047a"}, // Fixed-wing
		{ID: 1869, At: at(20), Domain: "aircraft", Identity: "8a047a"}, // Vehicle, resolved
		{ID: 1873, At: at(50), Domain: "aircraft", Identity: "8a047a"}, // Fixed-wing
		{ID: 1872, At: at(52), Domain: "aircraft", Identity: "8a047a"}, // Vehicle, resolved
	}

	passes := eventpass.Group(in, eventpass.DefaultGap)

	if len(passes) != 1 {
		t.Fatalf("got %d passes, want one aeroplane", len(passes))
	}
	if passes[0].ID != 1866 {
		t.Fatalf("pass ID %d, want the first detection 1866", passes[0].ID)
	}
	if passes[0].Identity != "8a047a" {
		t.Fatalf("identity %q, want 8a047a", passes[0].Identity)
	}
	if got := ids(&passes[0]); !equal(got, []uint{1866, 1867, 1868, 1869, 1873, 1872}) {
		t.Fatalf("detections %v, want all six in time order", got)
	}
}

// Most rows in a pass are never identified - four of the six above were not -
// so requiring an identity to join would scatter a pass back into its rows.
func TestGroupLetsUnidentifiedDetectionsJoin(t *testing.T) {
	t.Parallel()

	in := []eventpass.Detection{
		{ID: 1, At: at(0), Domain: "aircraft", Identity: "7cad4f"},
		{ID: 2, At: at(6), Domain: "aircraft"},
		{ID: 3, At: at(11), Domain: "aircraft"},
	}

	passes := eventpass.Group(in, eventpass.DefaultGap)

	if len(passes) != 1 || len(passes[0].Detections) != 3 {
		t.Fatalf("got %d passes with %v, want one holding all three", len(passes), ids(&passes[0]))
	}
	if passes[0].Identity != "7cad4f" {
		t.Fatalf("identity %q, want the one identity present to name the pass", passes[0].Identity)
	}
}

// The safeguard. Thirteen groups that morning each named exactly one aircraft,
// but two aeroplanes inside half a minute is ordinary at a busier hour, and
// timing alone cannot tell them apart. An authority can, and when it
// contradicts the timing it is right.
func TestGroupSplitsWhenTwoAircraftAreNamed(t *testing.T) {
	t.Parallel()

	in := []eventpass.Detection{
		{ID: 1, At: at(0), Domain: "aircraft", Identity: "7cad4f"},
		{ID: 2, At: at(5), Domain: "aircraft", Identity: "7cad4f"},
		{ID: 3, At: at(10), Domain: "aircraft", Identity: "8a047a"},
		{ID: 4, At: at(15), Domain: "aircraft", Identity: "8a047a"},
	}

	passes := eventpass.Group(in, eventpass.DefaultGap)

	if len(passes) != 2 {
		t.Fatalf("got %d passes, want the two aircraft separated despite overlapping in time", len(passes))
	}
	if passes[0].Identity == passes[1].Identity {
		t.Fatalf("both passes name %q", passes[0].Identity)
	}
}

// A detection nobody identified keeps its own domain, and a Vehicle row that
// may genuinely be a car must not be folded into the aeroplane beside it.
func TestGroupKeepsAnUnresolvedDomainApart(t *testing.T) {
	t.Parallel()

	in := []eventpass.Detection{
		{ID: 1910, At: at(0), Domain: "aircraft", Identity: "7c6c96"},
		{ID: 1917, At: at(76), Domain: "vehicle"}, // Vehicle 0.77, nothing resolved it
	}

	passes := eventpass.Group(in, eventpass.DefaultGap)

	if len(passes) != 2 {
		t.Fatalf("got %d passes, want the unresolved vehicle left on its own", len(passes))
	}
}

// A silence longer than the gap is a second pass, even for the same aircraft:
// the rescue helicopter crossed twice in one night, 56 minutes apart.
func TestGroupSeparatesTwoPassesOfTheSameAircraft(t *testing.T) {
	t.Parallel()

	in := []eventpass.Detection{
		{ID: 1, At: at(0), Domain: "aircraft", Identity: "7c617e"},
		{ID: 2, At: at(3360), Domain: "aircraft", Identity: "7c617e"},
	}

	passes := eventpass.Group(in, eventpass.DefaultGap)

	if len(passes) != 2 {
		t.Fatalf("got %d passes, want two crossings of one aircraft", len(passes))
	}
}

// The gap must be measured between neighbours, not from the start: a pass runs
// longer than the gap and would otherwise be cut in the middle, which is where
// the aircraft is overhead and the microphone least certain.
func TestGroupMeasuresTheGapBetweenNeighbours(t *testing.T) {
	t.Parallel()

	in := []eventpass.Detection{
		{ID: 1, At: at(0), Domain: "aircraft"},
		{ID: 2, At: at(25), Domain: "aircraft"},
		{ID: 3, At: at(50), Domain: "aircraft"},
		{ID: 4, At: at(75), Domain: "aircraft"},
	}

	passes := eventpass.Group(in, eventpass.DefaultGap)

	if len(passes) != 1 {
		t.Fatalf("got %d passes over a 75s span with 25s gaps, want one", len(passes))
	}
}

func TestGroupHandlesUnsortedInput(t *testing.T) {
	t.Parallel()

	in := []eventpass.Detection{
		{ID: 3, At: at(11), Domain: "aircraft"},
		{ID: 1, At: at(0), Domain: "aircraft"},
		{ID: 2, At: at(6), Domain: "aircraft"},
	}

	passes := eventpass.Group(in, eventpass.DefaultGap)

	if len(passes) != 1 {
		t.Fatalf("got %d passes, want one", len(passes))
	}
	if got := ids(&passes[0]); !equal(got, []uint{1, 2, 3}) {
		t.Fatalf("detections %v, want them sorted by time", got)
	}
}

func TestPassIDsAnnotatesEveryDetection(t *testing.T) {
	t.Parallel()

	in := []eventpass.Detection{
		{ID: 10, At: at(0), Domain: "aircraft", Identity: "abc"},
		{ID: 11, At: at(5), Domain: "aircraft"},
		{ID: 12, At: at(600), Domain: "aircraft"},
	}

	got := eventpass.PassIDs(in, eventpass.DefaultGap)

	if got[10] != 10 || got[11] != 10 {
		t.Fatalf("detections 10 and 11 map to %d and %d, want both to pass 10", got[10], got[11])
	}
	if got[12] != 12 {
		t.Fatalf("detection 12 maps to pass %d, want its own", got[12])
	}
}

func TestGroupOnNothing(t *testing.T) {
	t.Parallel()

	if passes := eventpass.Group(nil, eventpass.DefaultGap); passes != nil {
		t.Fatalf("got %v, want nil", passes)
	}
}
