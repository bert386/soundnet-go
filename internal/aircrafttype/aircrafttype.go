// Package aircrafttype maps an ICAO type designator to the kind of engine it
// has, which is what a microphone can actually tell apart.
//
// ADS-B names the aircraft - B738, C208, A139 - and the operator wants the
// station to report jet, prop and helicopter. Those are different questions:
// the designator is an identity, the engine class is a sound. This table is the
// bridge, and it is the label source for M4, the head that will one day hear
// the difference without ADS-B.
//
// The table is a CSV rather than a Go map so the offline tooling reads the very
// same file: the backfill script that builds M4's corpus runs in Python on the
// station, and a second copy of this mapping would drift from the first.
package aircrafttype

import (
	_ "embed"
	"encoding/csv"
	"fmt"
	"strings"
	"sync"
)

//go:embed engine_types.csv
var engineTypesCSV string

// Engine is the ICAO Doc 8643 engine type, narrowed to what matters here.
type Engine string

const (
	EngineJet        Engine = "jet"
	EngineTurboprop  Engine = "turboprop"
	EnginePiston     Engine = "piston"
	EngineHelicopter Engine = "helicopter"
)

// Coarse is the class M4 is trained on. Turboprop and piston are both "prop":
// the propeller's blade-pass tone is what the microphone hears.
type Coarse string

const (
	CoarseJet        Coarse = "jet"
	CoarseProp       Coarse = "prop"
	CoarseHelicopter Coarse = "helicopter"
)

// Type is one row of the table.
type Type struct {
	Designator string
	Engine     Engine
	Coarse     Coarse
	Name       string
}

var (
	loadOnce sync.Once
	table    map[string]Type
	loadErr  error
)

// Lookup returns the engine class for an ICAO type designator.
//
// Unknown designators return ok=false rather than a guess. A wrong engine class
// is a wrong training label, and the corpus has no way to notice one.
func Lookup(designator string) (Type, bool) {
	loadOnce.Do(func() { table, loadErr = parse(engineTypesCSV) })
	if loadErr != nil {
		return Type{}, false
	}
	t, ok := table[strings.ToUpper(strings.TrimSpace(designator))]
	return t, ok
}

// All returns every row, for tests and tooling.
func All() ([]Type, error) {
	loadOnce.Do(func() { table, loadErr = parse(engineTypesCSV) })
	if loadErr != nil {
		return nil, loadErr
	}
	out := make([]Type, 0, len(table))
	for _, t := range table {
		out = append(out, t)
	}
	return out, nil
}

func parse(data string) (map[string]Type, error) {
	var body strings.Builder
	for line := range strings.SplitSeq(data, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
	records, err := csv.NewReader(strings.NewReader(body.String())).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("aircrafttype: parse table: %w", err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("aircrafttype: empty table")
	}

	out := make(map[string]Type, len(records)-1)
	for i, r := range records[1:] {
		if len(r) != 4 {
			return nil, fmt.Errorf("aircrafttype: row %d has %d fields, want 4", i+2, len(r))
		}
		t := Type{
			Designator: strings.ToUpper(strings.TrimSpace(r[0])),
			Engine:     Engine(strings.TrimSpace(r[1])),
			Coarse:     Coarse(strings.TrimSpace(r[2])),
			Name:       strings.TrimSpace(r[3]),
		}
		if _, dup := out[t.Designator]; dup {
			return nil, fmt.Errorf("aircrafttype: %s listed twice", t.Designator)
		}
		out[t.Designator] = t
	}
	return out, nil
}
