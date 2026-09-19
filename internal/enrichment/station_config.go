package enrichment

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// StationConfig is the operator-supplied listening position, as it arrives from
// the web settings page.
//
// It is deliberately separate from Station: this type accepts what a human
// types, including degrees-minutes-seconds, and Station holds the validated
// decimal result the geometry uses. Parsing failures therefore surface at
// configuration time with a message, rather than as a silently wrong position.
type StationConfig struct {
	// Latitude and Longitude accept decimal degrees ("-34.11159") or
	// degrees-minutes-seconds ("34 06 41.7 S"). Both forms are common: mapping
	// sites hand out decimal, while charts and GPS units often show DMS.
	Latitude  string `json:"latitude" yaml:"latitude"`
	Longitude string `json:"longitude" yaml:"longitude"`

	// ElevationM is metres above sea level. Manual entry, because browser
	// geolocation altitude is unreliable and this value feeds slant range
	// directly: an error here biases every acoustic-lag correction.
	ElevationM float64 `json:"elevation_m" yaml:"elevation_m"`
}

// dmsPattern matches degrees-minutes-seconds with an optional hemisphere letter,
// tolerating the various separators people actually type: 34°06'41.7"S,
// 34 06 41.7 S, 34:06:41.7S.
var dmsPattern = regexp.MustCompile(
	`^\s*(-?\d+(?:\.\d+)?)\s*[^\w.-]*\s*(\d+(?:\.\d+)?)?\s*[^\w.-]*\s*(\d+(?:\.\d+)?)?\s*["']?\s*([NSEWnsew])?\s*$`)

// ParseCoordinate converts a decimal or DMS coordinate string to decimal degrees.
//
// A hemisphere letter wins over a leading minus sign, because "34 06 41.7 S" and
// "-34 06 41.7" are both meant as southern latitudes and a reading that returned
// a northern one for the first would be silently, confidently wrong.
func ParseCoordinate(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("enrichment: coordinate is empty")
	}

	// Plain decimal first: the common case, and unambiguous.
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		return v, nil
	}

	m := dmsPattern.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("enrichment: cannot parse coordinate %q as decimal degrees or degrees-minutes-seconds", s)
	}

	deg, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, fmt.Errorf("enrichment: bad degrees in %q: %w", s, err)
	}
	var minutes, seconds float64
	if m[2] != "" {
		if minutes, err = strconv.ParseFloat(m[2], 64); err != nil {
			return 0, fmt.Errorf("enrichment: bad minutes in %q: %w", s, err)
		}
	}
	if m[3] != "" {
		if seconds, err = strconv.ParseFloat(m[3], 64); err != nil {
			return 0, fmt.Errorf("enrichment: bad seconds in %q: %w", s, err)
		}
	}
	if minutes >= 60 || seconds >= 60 {
		return 0, fmt.Errorf("enrichment: minutes and seconds must be below 60 in %q", s)
	}

	negative := math.Signbit(deg)
	value := math.Abs(deg) + minutes/60 + seconds/3600
	switch strings.ToUpper(m[4]) {
	case "S", "W":
		negative = true
	case "N", "E":
		negative = false
	}
	if negative {
		value = -value
	}
	return value, nil
}

// FormatDMS renders decimal degrees as degrees-minutes-seconds for display
// beside the decimal value, so an operator can sanity-check against a chart.
func FormatDMS(value float64, isLatitude bool) string {
	hemisphere := "N"
	if isLatitude && value < 0 {
		hemisphere = "S"
	} else if !isLatitude {
		hemisphere = "E"
		if value < 0 {
			hemisphere = "W"
		}
	}
	v := math.Abs(value)
	deg := math.Floor(v)
	minFloat := (v - deg) * 60
	minutes := math.Floor(minFloat)
	seconds := (minFloat - minutes) * 60
	return fmt.Sprintf(`%d°%02d'%05.2f"%s`, int(deg), int(minutes), seconds, hemisphere)
}

// Resolve validates the configuration and returns the Station the geometry uses.
//
// Validation is strict and happens once, at load, because every downstream
// consumer treats a Station as trustworthy. The errors name the field and the
// value so a mistyped coordinate is obvious in a log rather than appearing later
// as inexplicably poor matching.
func (c StationConfig) Resolve() (Station, error) {
	lat, err := ParseCoordinate(c.Latitude)
	if err != nil {
		return Station{}, fmt.Errorf("station latitude: %w", err)
	}
	lon, err := ParseCoordinate(c.Longitude)
	if err != nil {
		return Station{}, fmt.Errorf("station longitude: %w", err)
	}
	if lat < -90 || lat > 90 {
		return Station{}, fmt.Errorf("station latitude %v is outside -90..90", lat)
	}
	if lon < -180 || lon > 180 {
		return Station{}, fmt.Errorf("station longitude %v is outside -180..180", lon)
	}
	// Dead Sea shore is about -430m; Everest is 8849m. Anything outside that is
	// a units mistake - feet entered as metres, most likely - not a real site.
	if c.ElevationM < -500 || c.ElevationM > 9000 {
		return Station{}, fmt.Errorf("station elevation %vm is implausible; expected metres above sea level", c.ElevationM)
	}

	s := Station{Latitude: lat, Longitude: lon, ElevationM: c.ElevationM}
	if !s.Valid() {
		// Catches exactly (0,0): syntactically fine, but it is the Gulf of
		// Guinea, and in practice it means the operator never set a position.
		return Station{}, fmt.Errorf("station coordinates are unset; enrichment cannot run without a real position")
	}
	return s, nil
}

// Configured reports whether the operator has supplied anything at all. Used to
// keep enrichment and the auto-labelling collector inert, and to say so in the
// UI, rather than failing obscurely at the first detection.
func (c StationConfig) Configured() bool {
	return strings.TrimSpace(c.Latitude) != "" && strings.TrimSpace(c.Longitude) != ""
}
