package autolabel

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FileCorpus writes labelled samples to a directory tree.
//
// Layout groups by aircraft type, because that is the axis the M4 head is
// trained on and it makes the corpus inspectable by eye:
//
//	<root>/B738/20260919T2251_7c7801_QFA557.wav
//	<root>/B738/20260919T2251_7c7801_QFA557.json
//	<root>/_untyped/...
//
// Samples whose type could not be resolved go to _untyped rather than being
// discarded. The audio and the transponder identity are still correct, so the
// example stays usable for anything that does not need the type - and the type
// can be filled in later from a better aircraft database.
type FileCorpus struct {
	Root string
}

// Write persists one sample as a WAV file plus a JSON sidecar.
//
// A sidecar rather than metadata embedded in the WAV: the labels are the point
// of this corpus, and they should be greppable and diffable without a parser.
func (f *FileCorpus) Write(_ context.Context, s *Sample) (string, error) {
	if s == nil {
		return "", fmt.Errorf("autolabel: nil sample")
	}
	if len(s.PCM) == 0 {
		return "", fmt.Errorf("autolabel: sample has no audio")
	}

	typeCode := "_untyped"
	if v, ok := s.Aircraft.Attributes["type_code"].(string); ok && v != "" {
		typeCode = sanitise(v)
	}
	dir := filepath.Join(f.Root, typeCode)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("autolabel: create corpus dir: %w", err)
	}

	base := fmt.Sprintf("%s_%s",
		s.CapturedAt.UTC().Format("20060102T150405"),
		sanitise(s.Aircraft.ICAO24))
	if cs := sanitise(strings.TrimSpace(s.Aircraft.Callsign)); cs != "" {
		base += "_" + cs
	}

	wavPath := filepath.Join(dir, base+".wav")
	if err := os.WriteFile(wavPath, wavBytes(s), 0o644); err != nil {
		return "", fmt.Errorf("autolabel: write clip: %w", err)
	}

	meta := map[string]any{
		"captured_at":      s.CapturedAt.UTC().Format(time.RFC3339),
		"icao24":           s.Aircraft.ICAO24,
		"callsign":         s.Aircraft.Callsign,
		"altitude_m":       s.Aircraft.AltitudeM,
		"slant_range_m":    s.Aircraft.SlantM,
		"acoustic_lag_ms":  s.Aircraft.Lag.Milliseconds(),
		"ground_speed_ms":  s.Aircraft.GroundSpMS,
		"track_deg":        s.Aircraft.TrackDeg,
		"station":          map[string]float64{"lat": s.Station.Latitude, "lon": s.Station.Longitude, "elevation_m": s.Station.ElevationM},
		"sample_rate":      s.SampleRate,
		"bit_depth":        s.BitDepth,
		"channels":         s.NumChannels,
		"label_provenance": "adsb-broadcast",
		"selection_reason": s.Reason,
	}
	maps.Copy(meta, s.Aircraft.Attributes)
	blob, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return "", fmt.Errorf("autolabel: encode labels: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, base+".json"), blob, 0o644); err != nil {
		return "", fmt.Errorf("autolabel: write labels: %w", err)
	}
	return wavPath, nil
}

// sanitise strips anything that would make a filename awkward.
func sanitise(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			// Aircraft type strings contain slashes and spaces ("737NG 838/W"),
			// which would create stray directories or unquotable paths.
			b.WriteRune('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

// wavBytes wraps raw PCM in a minimal RIFF/WAVE container.
//
// A container rather than raw PCM because every training pipeline and audio
// tool reads WAV, and a bare .pcm file loses the sample rate - which would make
// the corpus useless the moment anyone forgets what it was recorded at.
func wavBytes(s *Sample) []byte {
	channels := s.NumChannels
	if channels <= 0 {
		channels = 1
	}
	bits := s.BitDepth
	if bits <= 0 {
		bits = 16
	}
	rate := s.SampleRate
	if rate <= 0 {
		rate = 48000
	}

	blockAlign := channels * bits / 8
	byteRate := rate * blockAlign
	out := make([]byte, 0, 44+len(s.PCM))

	put32 := func(v uint32) { var b [4]byte; binary.LittleEndian.PutUint32(b[:], v); out = append(out, b[:]...) }
	put16 := func(v uint16) { var b [2]byte; binary.LittleEndian.PutUint16(b[:], v); out = append(out, b[:]...) }

	out = append(out, 'R', 'I', 'F', 'F')
	put32(uint32(36 + len(s.PCM)))
	out = append(out, 'W', 'A', 'V', 'E', 'f', 'm', 't', ' ')
	put32(16)
	put16(1) // PCM
	put16(uint16(channels))
	put32(uint32(rate))
	put32(uint32(byteRate))
	put16(uint16(blockAlign))
	put16(uint16(bits))
	out = append(out, 'd', 'a', 't', 'a')
	put32(uint32(len(s.PCM)))
	return append(out, s.PCM...)
}
