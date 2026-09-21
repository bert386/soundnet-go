package eventpass

import (
	"testing"
	"time"
)

// The case the operator reported: one flight, three categories.
//
// A single pass of QF642 leaves rows under aircraft, vehicle and weather,
// because each model reached for a different class in each three-second window
// and only some of them were put to ADS-B. Before absorption this made three
// entries in the list for one aeroplane.
func TestGroupAbsorbsAmbiguousClassesIntoTheIdentifiedPass(t *testing.T) {
	base := time.Date(2026, 9, 21, 6, 39, 56, 0, time.UTC)
	at := func(seconds int) time.Time { return base.Add(time.Duration(seconds) * time.Second) }

	detections := []Detection{
		// The aircraft rows ADS-B answered for.
		{ID: 1, At: at(0), Domain: "aircraft", Identity: "7c6d9f",
			Candidates: []string{"aircraft"}},
		{ID: 2, At: at(14), Domain: "aircraft", Identity: "7c6d9f",
			Candidates: []string{"aircraft"}},
		// Heard as road traffic, nothing identified it, ambiguous with aircraft.
		{ID: 3, At: at(6), Domain: "vehicle",
			Candidates: []string{"vehicle", "aircraft"}},
		// Heard as a storm. Fifty of these have been reviewed at this station
		// and every one was an aeroplane.
		{ID: 4, At: at(20), Domain: "weather",
			Candidates: []string{"weather", "aircraft"}},
		// A cat, during the flypast. Not ambiguous with anything, so it must
		// survive as its own pass however close it falls.
		{ID: 5, At: at(10), Domain: "biological",
			Candidates: []string{"biological"}},
	}

	passes := Group(detections, DefaultGap)

	var aircraft, biological *Pass
	for i := range passes {
		switch passes[i].Domain {
		case "aircraft":
			aircraft = &passes[i]
		case "biological":
			biological = &passes[i]
		default:
			t.Errorf("a %q pass survived; its detections should have joined the aircraft",
				passes[i].Domain)
		}
	}

	if aircraft == nil {
		t.Fatalf("no aircraft pass: %+v", passes)
	}
	if len(aircraft.Detections) != 4 {
		t.Errorf("aircraft pass holds %d detections, want 4 (two aircraft rows, the "+
			"vehicle and the thunderstorm): %+v", len(aircraft.Detections), aircraft.Detections)
	}
	if aircraft.Identity != "7c6d9f" {
		t.Errorf("aircraft pass identity = %q, want the hex ADS-B named", aircraft.Identity)
	}

	if biological == nil {
		t.Fatal("the cat was absorbed into the aeroplane")
	}
	if len(biological.Detections) != 1 {
		t.Errorf("biological pass holds %d detections, want 1", len(biological.Detections))
	}
}

// An authority is not overruled by a neighbour. A row that names a different
// aircraft stays its own pass however close it falls.
func TestGroupDoesNotAbsorbAnIdentifiedDetection(t *testing.T) {
	base := time.Date(2026, 9, 21, 6, 0, 0, 0, time.UTC)

	passes := Group([]Detection{
		{ID: 1, At: base, Domain: "aircraft", Identity: "aaaaaa",
			Candidates: []string{"aircraft"}},
		{ID: 2, At: base.Add(5 * time.Second), Domain: "vehicle", Identity: "bbbbbb",
			Candidates: []string{"vehicle", "aircraft"}},
	}, DefaultGap)

	if len(passes) != 2 {
		t.Fatalf("got %d passes, want 2 - two authorities named two different things: %+v",
			len(passes), passes)
	}
}

// Two aeroplanes a minute apart, with one ambiguous row between them. It
// belongs to whichever was nearer, not to whichever came out of the map first.
func TestGroupAbsorbsIntoTheNearerPass(t *testing.T) {
	base := time.Date(2026, 9, 21, 6, 0, 0, 0, time.UTC)
	at := func(seconds int) time.Time { return base.Add(time.Duration(seconds) * time.Second) }

	detections := []Detection{
		{ID: 1, At: at(0), Domain: "aircraft", Identity: "aaaaaa",
			Candidates: []string{"aircraft"}},
		{ID: 2, At: at(90), Domain: "aircraft", Identity: "bbbbbb",
			Candidates: []string{"aircraft"}},
		// Twenty seconds after the second aeroplane, seventy after the first.
		{ID: 3, At: at(110), Domain: "weather",
			Candidates: []string{"weather", "aircraft"}},
	}

	grouped := Group(detections, DefaultGap)
	for _, p := range grouped {
		if p.Identity != "bbbbbb" {
			continue
		}
		if len(p.Detections) != 2 {
			t.Fatalf("the nearer aeroplane holds %d detections, want 2: %+v",
				len(p.Detections), p.Detections)
		}
		return
	}
	t.Fatalf("no pass for the nearer aeroplane: %+v", grouped)
}

// A caller that supplies no candidates gets exactly the grouping this package
// did before absorption existed. That is what keeps the field additive.
func TestGroupWithoutCandidatesIsUnchanged(t *testing.T) {
	base := time.Date(2026, 9, 21, 6, 0, 0, 0, time.UTC)

	passes := Group([]Detection{
		{ID: 1, At: base, Domain: "aircraft", Identity: "aaaaaa"},
		{ID: 2, At: base.Add(5 * time.Second), Domain: "weather"},
	}, DefaultGap)

	if len(passes) != 2 {
		t.Fatalf("got %d passes, want 2 - nothing said the weather row could be an "+
			"aircraft: %+v", len(passes), passes)
	}
}
