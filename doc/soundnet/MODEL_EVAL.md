# Model evaluation: CED-tiny against YAMNet, on this station's audio

Measured 2026-09-20 against the operator-labelled clips in GROUND_TRUTH.md.

The point of measuring here rather than reading a benchmark is that AudioSet mAP
was computed on someone else's corpus at someone else's signal-to-noise ratio.
The question that matters is narrower: on these clips, at this station, does the
model put an aircraft in an aircraft class and leave everything else alone?

## The candidates

| | params | AudioSet mAP | notes |
|---|---|---|---|
| YAMNet (running) | 3.7M | 0.306 | 2019 MobileNetV1, TFLite, 521 classes |
| CED-tiny | 5.5M | 0.481 | ViT distilled, ONNX, 527 classes |
| CED-mini | 9.6M | 0.490 | |
| mn10_as (EfficientAT) | 4.9M | 0.471 | MobileNetV3, needs a PyTorch export |

CED ships as ready-made ONNX from sherpa-onnx (`model.onnx` 22 MB, **`model.int8.onnx`
6.1 MB**), with the standard AudioSet `class_labels_indices.csv`. The labels are
AudioSet display names, which is what `internal/eventclass` already holds - so
the 521-vs-527 index problem is solved by joining on name, not index.

## Result: CED-tiny is far better at discrimination

Seven labelled aircraft clips and ten labelled non-aircraft clips. "Aircraft
score" is the highest of the six AudioSet aircraft classes.

| id | truth | YAMNet said | CED top-1 | CED aircraft |
|---|---|---|---|---|
| 805 | turbo-prop | Propeller 0.41 | Vehicle 0.68 | **0.646** |
| 828 | close propeller | Propeller 0.50 | Vehicle 0.42 | **0.367** |
| 759 | propeller overhead | **Vehicle 0.80** | Vehicle 0.38 | **0.374** |
| 905 | large truck | **Vehicle 0.80** | Vehicle 0.58 | **0.000** |
| 817 | human whistling | **Police car (siren) 0.74** | **Whistling 0.35** | 0.000 |
| 726 | high aircraft/traffic | Cat 0.85 | Animal 0.35 | 0.000 |
| 730 | distant aircraft | Chicken 0.59 | Animal 0.28 | 0.000 |
| 733 | distant propeller | Chirp tone 0.50 | Bird 0.56 | 0.000 |
| 824 | commercial jet | Thunderstorm 0.80 | Bird 0.25 | 0.000 |

**Zero false positives across all ten non-aircraft clips.**

Three lines carry the finding:

- **905 and 759 are both `Vehicle` to YAMNet at 0.80, and are a lorry and an
  aeroplane.** CED gives the lorry an aircraft score of **0.000** and the
  aeroplane **0.374**. It separates acoustically what YAMNet could not.
- **817** is a person whistling, which YAMNet called a police siren at 0.74.
  CED calls it `Whistling`.
- The four misses are all the operator's *distant* or *high* ones. Those are not
  a model problem.

### This corrects a claim in GROUND_TRUTH.md

That document says presence is recoverable but type is not, and that only ADS-B
can tell a truck from an aircraft. That was true **of YAMNet**, and was read as
true of the problem. A 2023 model does draw the distinction on the same audio.
ADS-B remains the only source of *identity* - registration, type, altitude - and
the only thing that can help at all with the faint ones.

## Low-pass helps. Normalising after it does not.

GROUND_TRUTH.md recommends low-passing at 1.2 kHz **then normalising**, on the
strength of a jet going 0.000 -> 0.332. That measurement only ever looked at
clips containing aircraft. Run over the negatives as well, the normalisation is
actively harmful:

| normalisation cap | aircraft detected | worst non-aircraft |
|---|---|---|
| **none (low-pass only)** | **3/7** | **0.137** |
| 6 dB | 3/7 | 0.167 |
| 12 dB | 3/7 | 0.185 |
| 18 dB | 3/7 | 0.200 |
| uncapped | 3/7 | 0.236 |

Normalisation never converts a miss into a detection, and it raises the noise
floor monotonically. The reason is visible in the gain each clip asks for: the
clips with an aircraft in them need 9-13 dB, the ones with nothing in them need
28-31 dB. Thirty decibels of nothing is a broadband low-frequency rumble, and
rumble is what an aircraft sounds like - after the transform, rustling dry grass
is `Vehicle 0.53`.

**So: low-pass at 1.2 kHz, no normalisation.** Against the raw clips that lifts
the three detected aircraft from 0.374/0.646/0.367 to 0.556/0.714/0.638, and
lifts the four missed ones off zero to 0.09-0.13 - still short, but no longer
invisible. The best separation of any configuration tested.

This is the same trap as the category filter, in a new place: a transform that
improves the positives and is never checked against the negatives is
indistinguishable from one that improves nothing.

## What is still missing, and what fixes it

Four of seven aircraft stay under 0.30 in every configuration. They are the
distant ones, and no amount of model or preprocessing rescued them.

The design already has the answer and it is now live: **0.15 alone never clears
a sensible threshold; 0.15 beside an ADS-B contact at a credible slant range is
a confident detection.** As of today that corroboration works end to end - see
detection 990, a Piper PA-28-161 matched at 1.6 km. Lowering the acoustic bar
for a domain where an authority can confirm is worth more here than any further
accuracy.

## Cost

CED-tiny scored a 15-second clip in **0.073 s** on x86 (RTF 0.005), single
thread, fp32. The int8 model is 6.1 MB. A Pi 4 is perhaps 10-20x slower, which
puts a 3-second window at roughly 150-300 ms against a budget where BirdNET
already spends 191 ms. Affordable.

## The fused export, and what it is verified against

**Done 2026-09-20.** `ced_tiny_fused_single.onnx`, 22.8 MB, md5
`16ac7af2972101d473679dd6ecbfc721`. Raw 3-second 16 kHz mono waveform in, 527
AudioSet probabilities out, front-end inside the graph exactly as BirdNET v3 and
BSG do it. Produced by `eval/export_ced_fused.py`.

The front-end was never a guess in the end. CED's own repository ships
`onnx_inference_with_torchaudio.py`, which states it exactly:

    MelSpectrogram(f_min=0, sample_rate=16000, win_length=512, center=False,
                   n_fft=512, f_max=8000, hop_length=160, n_mels=64)
    AmplitudeToDB(top_db=120)

That is **torchaudio, not kaldi fbank**. sherpa-onnx computes kaldi features in
C++, so sherpa is an approximation of the training recipe and the fused graph is
the recipe itself. Anyone inferring the front-end from sherpa - which is what
writing it in Go would have meant - would have implemented the approximation.

**Verified two ways, and the second is the one that matters.**

Against sherpa-onnx on 3-second windows: top-1 agrees on 7 of 10, median
absolute score difference 0.061 on the oracle's top class. Every disagreement is
a near-tie between adjacent classes - Duck, Bird and Fowl within 0.07 of each
other. That test cannot separate "the export is wrong" from "the two front-ends
differ", so on its own it proves little.

Against **PyTorch itself**, same weights and same front-end, twelve windows:

    worst |torch - onnx| = 1.3e-06, argmax identical on every window

float32 arithmetic noise. The export is faithful; the difference from sherpa is
the kaldi approximation, not a defect.

### Fixed length is the contract, not a limitation

CED interpolates its positional embeddings from the input length, so tracing
bakes the frame grid in and the graph is valid only at the window it was traced
with. Feeding a 15-second clip to a 3-second graph fails with a broadcast error,
which is at least loud rather than silent.

Three seconds is what SoundNet wants anyway: it is BirdNET's window, shared so
that a detection and its diagnostics describe the same audio, and YAMNet is
already driven at a fixed 15600 samples for the same reason.

## Prerequisite that was missing entirely

ONNX Runtime was not on the station. The Pi carried only
`libtensorflowlite_c.so`, so **no** ONNX model in the catalog could run there -
Perch v2 and BirdNET v3.0 as much as CED. Fixed on 2026-09-20 with ORT 1.25.1
aarch64 (19 MB) plus `birdnet.onnxruntimepath`; see STATE.md for the two traps.

## The obstacle as it was, before the export

CED's ONNX input is named `feats`: it takes a 64-band kaldi log-mel filterbank,
**not** raw audio. sherpa-onnx computes that in C++ outside the graph.

Every model in this repository has its front-end **inside** the graph - BirdNET
v3's mel is a Conv1d, v2.4 uses `dfttrunc`, BSG ships "fused" - and the Go side
only ever hands over raw audio. There is no mel filterbank in Go here; the only
FFT is `internal/acoustics/doppler.go`'s radix-2, used for Doppler.

So adopting CED means one of:

1. **Re-export with the front-end fused**, matching this repo's convention.
   Needs PyTorch once, offline; afterwards it is an ordinary catalog entry and
   the adapter is the same shape as the YAMNet one.
2. **Write the log-mel front-end in Go.** About 200 lines on top of the existing
   FFT, and it must match CED's configuration exactly - a subtly wrong window or
   mel scale produces a model that loads, runs, and returns plausible nonsense.
   That is this project's most familiar failure mode, so (1) is preferred.

## Reproducing

The harness is in `doc/soundnet/eval/`. It drives the prebuilt
`sherpa-onnx-offline-audio-tagging` CLI over clips fetched from the station's own
`/api/v2/audio/{id}`, so it needs nothing installed and no model conversion -
only the CLI tarball and the model tarball.

    levels_birds.py       peak/RMS/crest across recent clips; sets the gain ceiling
    ced_eval.py           CED against the operator's labels, raw clips
    ced_lowpass.py        low-pass then normalise, both sides of the set
    ced_capsweep.py       low-pass plus a swept normalisation cap
    export_ced_fused.py   the fused export
    verify_exact.py       fused ONNX against PyTorch - the correctness test
    verify_3s.py          fused ONNX against sherpa-onnx, 3 s windows

Python rather than Go on purpose: it is a measuring instrument, not part of
the station, and it has to stay easy to point at the next model. Worth
rebuilding as a `cmd/` tool only if model comparison becomes routine.
