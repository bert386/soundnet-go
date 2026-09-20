// Command clipspectrum reports the band energy of a clip.
//
// Diagnostic only. It answers the question the classifier cannot: is the sound
// actually in the recording? Aircraft energy is overwhelmingly below ~500 Hz,
// so if those bands are empty the problem is the capture chain, not the model.
package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/cmplx"
	"os"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: clipspectrum <clip.wav>...")
		os.Exit(2)
	}
	for _, path := range os.Args[1:] {
		report(path)
	}
}

var bands = []struct {
	lo, hi float64
	name   string
}{
	{20, 60, "20-60 Hz    (blade/prop fundamental)"},
	{60, 125, "60-125 Hz   (engine fundamental)"},
	{125, 250, "125-250 Hz  (aircraft harmonics)"},
	{250, 500, "250-500 Hz  (aircraft harmonics)"},
	{500, 1000, "500-1k Hz   (broadband jet)"},
	{1000, 2000, "1k-2k Hz    (low birdsong)"},
	{2000, 4000, "2k-4k Hz    (birdsong)"},
	{4000, 8000, "4k-8k Hz    (birdsong//insects)"},
}

func report(path string) {
	pcm, rate, channels, err := readWAV(path)
	if err != nil {
		fmt.Printf("%s: %v\n", path, err)
		return
	}
	samples := make([]float64, len(pcm)/2)
	var peak, rms float64
	for i := range samples {
		v := float64(int16(binary.LittleEndian.Uint16(pcm[i*2:]))) / 32768.0
		samples[i] = v
		peak = math.Max(peak, math.Abs(v))
		rms += v * v
	}
	rms = math.Sqrt(rms / float64(len(samples)))

	fmt.Printf("\n=== %s ===\n", path[strings.LastIndex(path, "/")+1:])
	fmt.Printf("  %d Hz, %d ch, %.1f s, peak %.3f (%.1f dBFS), rms %.4f (%.1f dBFS)\n",
		rate, channels, float64(len(samples))/float64(rate), peak, db(peak), rms, db(rms))

	// Average the magnitude spectrum over overlapping Hann-windowed frames, so a
	// steady low-frequency source is not lost among transient birdsong.
	const n = 8192
	energy := make([]float64, len(bands))
	frames := 0
	for start := 0; start+n <= len(samples); start += n / 2 {
		frames++
		buf := make([]complex128, n)
		for i := range n {
			w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1))
			buf[i] = complex(samples[start+i]*w, 0)
		}
		spec := fft(buf)
		for bi, b := range bands {
			lo := int(b.lo * float64(n) / float64(rate))
			hi := int(b.hi * float64(n) / float64(rate))
			var sum float64
			for k := lo; k < hi && k < n/2; k++ {
				sum += cmplx.Abs(spec[k])
			}
			energy[bi] += sum
		}
	}
	if frames == 0 {
		fmt.Println("  clip too short to analyse")
		return
	}

	var total float64
	for _, e := range energy {
		total += e
	}
	fmt.Printf("  %d frames, band distribution:\n", frames)
	for bi, b := range bands {
		share := 0.0
		if total > 0 {
			share = 100 * energy[bi] / total
		}
		bar := strings.Repeat("#", int(share/2))
		fmt.Printf("    %-34s %5.1f%%  %s\n", b.name, share, bar)
	}
}

func db(v float64) float64 {
	if v <= 0 {
		return math.Inf(-1)
	}
	return 20 * math.Log10(v)
}

// fft is a radix-2 Cooley-Tukey transform; len(x) must be a power of two.
func fft(x []complex128) []complex128 {
	n := len(x)
	if n <= 1 {
		return x
	}
	even := make([]complex128, n/2)
	odd := make([]complex128, n/2)
	for i := range n / 2 {
		even[i] = x[2*i]
		odd[i] = x[2*i+1]
	}
	even = fft(even)
	odd = fft(odd)
	out := make([]complex128, n)
	for k := range n / 2 {
		t := cmplx.Exp(complex(0, -2*math.Pi*float64(k)/float64(n))) * odd[k]
		out[k] = even[k] + t
		out[k+n/2] = even[k] - t
	}
	return out
}

func readWAV(path string) (pcm []byte, rate, channels int, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, 0, err
	}
	if len(raw) < 44 || string(raw[0:4]) != "RIFF" {
		return nil, 0, 0, fmt.Errorf("not a RIFF file")
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
