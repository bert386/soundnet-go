package enrichment

import (
	"math"
	"time"
)

// SpeedOfSound in metres per second at roughly 20 degrees Celsius. The ~0.6 m/s
// per degree variation is negligible against ADS-B position error, so a constant
// is honest here rather than false precision.
const SpeedOfSound = 343.0

// earthRadiusM is the mean radius used for the horizontal distance calculation.
const earthRadiusM = 6371000.0

// Position is a point in the air.
type Position struct {
	Latitude  float64
	Longitude float64
	AltitudeM float64

	// GroundSpeedMS and TrackDeg describe motion, used to back-project where the
	// aircraft was when the sound was emitted.
	GroundSpeedMS float64
	TrackDeg      float64
}

// HorizontalDistanceM returns great-circle distance in metres between two
// surface points, by the haversine formula.
//
// Haversine rather than a flat-earth approximation not for accuracy over the few
// kilometres involved - both are fine there - but because it degrades sensibly
// if this is ever used with a distant aircraft, and it has no special cases
// near the antimeridian.
func HorizontalDistanceM(lat1, lon1, lat2, lon2 float64) float64 {
	p1 := lat1 * math.Pi / 180
	p2 := lat2 * math.Pi / 180
	dp := (lat2 - lat1) * math.Pi / 180
	dl := (lon2 - lon1) * math.Pi / 180

	a := math.Sin(dp/2)*math.Sin(dp/2) +
		math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * earthRadiusM * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

// SlantRangeM returns the straight-line distance from the station to a position,
// combining horizontal separation with the height difference.
//
// This, not the horizontal distance, is the path sound actually travelled. For
// an aircraft directly overhead at 10,000 ft the horizontal distance is nearly
// zero while the slant range is about 3 km - a nine second difference in
// propagation delay, which is more than enough to match the wrong aircraft.
func SlantRangeM(s Station, p Position) float64 {
	horizontal := HorizontalDistanceM(s.Latitude, s.Longitude, p.Latitude, p.Longitude)
	vertical := p.AltitudeM - s.ElevationM
	return math.Hypot(horizontal, vertical)
}

// AcousticLag returns how long sound took to travel from a position to the
// station.
func AcousticLag(s Station, p Position) time.Duration {
	return time.Duration(SlantRangeM(s, p) / SpeedOfSound * float64(time.Second))
}

// BackProject estimates where a moving object was, d before the time its
// position was reported, assuming constant velocity along its track.
//
// This is what makes single-query lag correction possible. The naive approach
// needs the aircraft's position at the emission time, which would mean a second
// API call at a different timestamp and a second credit against a 4,000 per day
// budget. Instead the reported position is rewound along its own track, which is
// accurate over the few seconds involved because aircraft do not manoeuvre
// sharply at cruise.
func BackProject(p Position, d time.Duration) Position {
	if p.GroundSpeedMS <= 0 || d <= 0 {
		return p
	}
	dist := p.GroundSpeedMS * d.Seconds()
	bearing := p.TrackDeg * math.Pi / 180

	// Move backwards along the track: north/east components of the reverse
	// bearing, converted to degrees of latitude and longitude.
	dNorth := -dist * math.Cos(bearing)
	dEast := -dist * math.Sin(bearing)

	latRad := p.Latitude * math.Pi / 180
	out := p
	out.Latitude = p.Latitude + (dNorth/earthRadiusM)*180/math.Pi
	// Longitude degrees shrink with latitude; without the cosine term an
	// aircraft at high latitude would be rewound far too far east or west.
	cosLat := math.Cos(latRad)
	if math.Abs(cosLat) > 1e-9 {
		out.Longitude = p.Longitude + (dEast/(earthRadiusM*cosLat))*180/math.Pi
	}
	return out
}

// CorrectForAcousticLag finds where an object was when it emitted the sound that
// was heard at the station, and how long that sound took to arrive.
//
// The correction is circular by nature: the lag depends on the distance, and the
// distance at emission time depends on the lag. It is solved by iteration, which
// converges in two or three passes because each correction is small relative to
// the last. The loop is bounded rather than run to a tolerance so the cost is
// predictable on a Raspberry Pi.
func CorrectForAcousticLag(s Station, reported Position) (emitted Position, lag time.Duration) {
	emitted = reported
	for range 3 {
		lag = AcousticLag(s, emitted)
		emitted = BackProject(reported, lag)
	}
	return emitted, lag
}

// MatchQuality scores how well a position explains a sound heard at the station,
// from 0 to 1.
//
// Two things make a good match: the object was close, and it was overhead rather
// than off on the horizon. Distance dominates, because a nearer aircraft is both
// louder and more likely to be the one actually heard.
//
// maxRangeM is the distance beyond which a match is not credible at all. It is a
// parameter rather than a constant because it depends on the site: a quiet rural
// station hears aircraft much further away than an urban one.
func MatchQuality(s Station, p Position, maxRangeM float64) float64 {
	if maxRangeM <= 0 {
		return 0
	}
	slant := SlantRangeM(s, p)
	if slant > maxRangeM {
		return 0
	}
	// Linear falloff with range. Deliberately not inverse-square: this is a
	// ranking heuristic among candidates, not a physical model of loudness, and
	// a sharper curve would make the top match's score meaninglessly small.
	rangeScore := 1 - slant/maxRangeM

	// Elevation angle: directly overhead scores best. An aircraft at the same
	// slant range but low on the horizon is more likely masked by terrain and
	// less likely to be the one heard.
	horizontal := HorizontalDistanceM(s.Latitude, s.Longitude, p.Latitude, p.Longitude)
	vertical := p.AltitudeM - s.ElevationM
	elevationScore := 0.0
	if slant > 0 && vertical > 0 {
		elevationScore = vertical / slant // sin of the elevation angle
	}
	_ = horizontal

	return 0.75*rangeScore + 0.25*elevationScore
}

// MaxCredibleLag bounds how much acoustic delay the back-projection is trusted
// over.
//
// The figure is not arbitrary. A live OpenSky sample at the deployment station
// showed an airliner at 14.7 km slant range, which is a 42.9 second acoustic
// delay - during which a jet at 250 m/s covers nearly 11 km. Rewinding a
// straight-line track that far assumes no turn and no speed change for
// three quarters of a minute, which is not a safe assumption on approach.
//
// Twenty seconds corresponds to roughly 6.9 km of slant range, which is also
// about as far as an airliner is reliably audible over background noise, so the
// bound costs little in practice.
const MaxCredibleLag = 20 * time.Second

// LagIsCredible reports whether a computed lag is small enough for the
// back-projected position to be trusted.
//
// Callers must treat a false result as "no match" rather than matching anyway
// with lower confidence: beyond this range the projected position can be
// kilometres wrong, so a match made from it is not a weak identification but a
// potentially confident wrong one.
func LagIsCredible(lag time.Duration) bool {
	return lag > 0 && lag <= MaxCredibleLag
}
