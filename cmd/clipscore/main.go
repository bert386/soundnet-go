// Command clipscore runs YAMNet over a recorded clip exactly as the adapter
// does, and prints what it scored.
//
// Diagnostic only: it exists to check the adapter's behaviour against clips
// whose true content is known, which is the only way to tell "the model cannot
// hear it" apart from "the taxonomy filter threw it away".
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/csv"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/bert386/soundnet-go/internal/audiocore/resample"
	tflitelib "github.com/tphakala/go-tflite"
)

const (
	frameSamples = 15600
	framesPerWin = 4
	targetRate   = 16000
)

func main() {
	// run() owns the deferred interpreter teardown; main only decides the exit
	// code, so os.Exit can never skip a Delete.
	if err := run(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 4 {
		return errors.New("usage: clipscore <model.tflite> <class_map.csv> <clip.wav> [clip.wav]")
	}
	labels := loadLabels(os.Args[2])

	model := tflitelib.NewModelFromFile(os.Args[1])
	if model == nil {
		return errors.New("failed to load model")
	}
	defer model.Delete()
	opts := tflitelib.NewInterpreterOptions()
	defer opts.Delete()
	opts.SetNumThread(4)
	interp := tflitelib.NewInterpreter(model, opts)
	defer interp.Delete()
	if interp.AllocateTensors() != tflitelib.OK {
		return errors.New("AllocateTensors failed")
	}

	for _, path := range os.Args[3:] {
		scoreClip(interp, labels, path)
	}
	return nil
}

// scoreClip reads a clip, optionally preprocesses it, and prints what YAMNet
// scored. Deliberately linear - read, resample, filter, normalise, frame,
// score, print - because a diagnostic is easier to trust when it reads top to
// bottom, and splitting it to satisfy a complexity metric would make the one
// thing it does harder to follow.
//
//nolint:gocognit,gocyclo // linear by design; see the comment above.
func scoreClip(interp *tflitelib.Interpreter, labels []string, path string) {
	pcm, rate, channels, err := readWAV(path)
	if err != nil {
		fmt.Printf("%s: %v\n", path, err)
		return
	}
	fmt.Printf("\n=== %s ===\n", path[strings.LastIndex(path, "/")+1:])
	fmt.Printf("  %d bytes PCM, %d Hz, %d channel(s), %.2f s\n",
		len(pcm), rate, channels, float64(len(pcm))/float64(rate*channels*2))

	if rate != targetRate {
		r, rErr := resample.NewResampler(rate, targetRate)
		if rErr != nil {
			fmt.Printf("  resampler: %v\n", rErr)
			return
		}
		out, rsErr := r.ResampleInto(pcm)
		if rsErr != nil {
			fmt.Printf("  resample: %v\n", rsErr)
			return
		}
		pcm = bytes.Clone(out)
	}

	samples := make([]float32, len(pcm)/2)
	for i := range samples {
		samples[i] = float32(int16(binary.LittleEndian.Uint16(pcm[i*2:]))) / 32768.0
	}
	fmt.Printf("  %d samples at %d Hz (%.2f s)\n", len(samples), targetRate,
		float64(len(samples))/targetRate)

	// SOUNDNET_LPF applies a low-pass before inference. Birdsong lives above
	// ~1.5 kHz and aircraft energy below ~500 Hz, so removing the high end tests
	// whether loud close birds are masking a quiet distant aircraft inside the
	// model rather than merely outranking it.
	if l := os.Getenv("SOUNDNET_LPF"); l != "" {
		cutoff, convErr := strconv.ParseFloat(l, 64)
		if convErr == nil && cutoff > 0 {
			// One-pole low-pass, applied forward and backward so the filter adds
			// no phase shift - which matters because YAMNet sees a spectrogram.
			rc := 1.0 / (2 * math.Pi * cutoff)
			dt := 1.0 / targetRate
			a := float32(dt / (rc + dt))
			for pass := range 2 {
				if pass == 1 {
					for i, j := 0, len(samples)-1; i < j; i, j = i+1, j-1 {
						samples[i], samples[j] = samples[j], samples[i]
					}
				}
				y := samples[0]
				for i := range samples {
					y += a * (samples[i] - y)
					samples[i] = y
				}
			}
			for i, j := 0, len(samples)-1; i < j; i, j = i+1, j-1 {
				samples[i], samples[j] = samples[j], samples[i]
			}
			fmt.Printf("  low-passed at %.0f Hz\n", cutoff)
		}
	}

	// SOUNDNET_RMS normalises to a target RMS instead of a target peak.
	//
	// Peak normalisation is hostage to the loudest transient: one close bird
	// chirp sets the peak and the quiet aircraft underneath it is lifted barely
	// at all. RMS tracks the sustained level, which is what an overflight is.
	if r := os.Getenv("SOUNDNET_RMS"); r != "" {
		target, convErr := strconv.ParseFloat(r, 64)
		if convErr == nil && target > 0 {
			var sum float64
			for _, s := range samples {
				sum += float64(s) * float64(s)
			}
			cur := math.Sqrt(sum / float64(len(samples)))
			if cur > 0 {
				scale := float32(target / cur)
				var clipped int
				for i := range samples {
					v := float64(samples[i] * scale)
					if v > 1 || v < -1 {
						clipped++
					}
					samples[i] = float32(math.Max(-1, math.Min(1, v)))
				}
				fmt.Printf("  rms %.4f -> %.3f (x%.1f, %d samples clipped %.2f%%)\n",
					cur, target, scale, clipped, 100*float64(clipped)/float64(len(samples)))
			}
		}
	}

	// SOUNDNET_GAIN scales the waveform before inference, to test whether a
	// quiet recording is the reason a clearly present sound is not classified.
	if g := os.Getenv("SOUNDNET_GAIN"); g != "" {
		gain, convErr := strconv.ParseFloat(g, 32)
		if convErr == nil && gain > 0 {
			var peak float32
			for _, s := range samples {
				if a := float32(math.Abs(float64(s))); a > peak {
					peak = a
				}
			}
			if gain < 0 {
				gain = 1
			}
			// "peak" mode: normalise so the loudest sample sits at the gain value.
			scale := float32(gain)
			if peak > 0 {
				scale = float32(gain) / peak
			}
			for i := range samples {
				v := samples[i] * scale
				samples[i] = float32(math.Max(-1, math.Min(1, float64(v))))
			}
			fmt.Printf("  normalised: peak %.3f -> %.2f (x%.1f)\n", peak, gain, scale)
		}
	}

	// Tile the whole clip with the adapter's 3 s windows, so a short event is
	// not averaged away by a long recording.
	winSamples := 3 * targetRate
	best := make([]float32, len(labels))
	windows := 0
	for start := 0; start+winSamples <= len(samples); start += winSamples {
		windows++
		scoreWindow(interp, samples[start:start+winSamples], best)
	}
	if windows == 0 && len(samples) >= frameSamples {
		windows = 1
		scoreWindow(interp, samples, best)
	}
	fmt.Printf("  %d analysis window(s)\n", windows)

	idx := make([]int, len(best))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return best[idx[a]] > best[idx[b]] })

	fmt.Println("  top 15 classes:")
	for _, i := range idx[:15] {
		fmt.Printf("    %-42s %.3f\n", labels[i], best[i])
	}

	fmt.Println("  aircraft-related classes specifically:")
	for _, want := range []string{
		"Aircraft", "Aircraft engine", "Jet engine", "Propeller, airscrew",
		"Helicopter", "Fixed-wing aircraft, airplane", "Engine",
		"Medium engine (mid frequency)", "Heavy engine (low frequency)",
	} {
		for i, l := range labels {
			if l == want {
				rank := 0
				for r, j := range idx {
					if j == i {
						rank = r + 1
						break
					}
				}
				fmt.Printf("    %-42s %.3f   (rank %d)\n", l, best[i], rank)
			}
		}
	}
}

func scoreWindow(interp *tflitelib.Interpreter, window, best []float32) {
	in := interp.GetInputTensor(0)
	out := interp.GetOutputTensor(0)
	span := len(window) - frameSamples
	for f := range framesPerWin {
		off := 0
		if span > 0 && framesPerWin > 1 {
			off = int(math.Round(float64(span) * float64(f) / float64(framesPerWin-1)))
		}
		if off+frameSamples > len(window) {
			break
		}
		copy(in.Float32s(), window[off:off+frameSamples])
		if interp.Invoke() != tflitelib.OK {
			return
		}
		for i, s := range out.Float32s() {
			if i < len(best) && s > best[i] {
				best[i] = s
			}
		}
	}
}

// readWAV reads a 16-bit PCM WAV, returning the data chunk, sample rate and
// channel count.
func readWAV(path string) (pcm []byte, rate, channels int, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, 0, err
	}
	if len(raw) < 44 || string(raw[0:4]) != "RIFF" || string(raw[8:12]) != "WAVE" {
		return nil, 0, 0, fmt.Errorf("not a RIFF/WAVE file")
	}
	pos := 12
	for pos+8 <= len(raw) {
		id := string(raw[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(raw[pos+4 : pos+8]))
		body := pos + 8
		switch id {
		case "fmt ":
			channels = int(binary.LittleEndian.Uint16(raw[body+2 : body+4]))
			rate = int(binary.LittleEndian.Uint32(raw[body+4 : body+8]))
		case "data":
			end := min(body+size, len(raw))
			return raw[body:end], rate, channels, nil
		}
		pos = body + size
		if size%2 == 1 {
			pos++
		}
	}
	return nil, 0, 0, fmt.Errorf("no data chunk")
}

func loadLabels(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		panic(err)
	}
	defer func() { _ = f.Close() }()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		panic(err)
	}
	out := make([]string, len(rows)-1)
	for _, r := range rows[1:] {
		i, convErr := strconv.Atoi(r[0])
		if convErr != nil {
			panic(convErr)
		}
		out[i] = r[2]
	}
	return out
}
