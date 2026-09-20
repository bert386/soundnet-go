# Ground truth: the 2026-09-20 aircraft set

Seven clips from the station microphone, labelled by the operator by ear at the
time. This is the only labelled non-bird data the project has, and it was
recorded before the taxonomy filter was deployed, which is the only reason the
clips exist at all - the build that captured them emitted every class above the
score floor, so an aircraft came through as whatever YAMNet happened to rank
first.

Clips live on the Pi under `~/soundnet/clips/2026/09/`. They are not committed:
seven 15-second WAVs at 48 kHz is ~10 MB, and git is a poor audio store.

| Time | Clip | Truth | YAMNet said |
|---|---|---|---|
| 10:51:02 | `chirp_59p_20260920T105104Z.wav` | propeller plane | Chirp tone 0.59 |
| 10:51:16 | `chirp_50p_20260920T105117Z.wav` | small propeller plane | Chirp tone 0.50 |
| 10:51:41 | `cat_85p_20260920T105143Z.wav` | aircraft | **Cat 0.85**, Meow 0.74 |
| 10:53:26 | `chirp_50p_20260920T105328Z.wav` | commercial jet | Chirp tone 0.50 |
| 10:53:41 | `chicken_50p_20260920T105343Z.wav` | jet, high overhead | Chicken, rooster 0.50 |
| 10:54:03 | `chirp_59p_20260920T105405Z.wav` | unlabelled | Chirp tone 0.59 |
| 10:55:02 | `chirp_50p_20260920T105504Z.wav` | helicopter | Chirp tone 0.50 |

Every clip also contains birdsong, close and loud; the aircraft is the quiet
thing underneath. The operator also reports that other `Cat` and
`Chicken, rooster` detections from the same period were hammering and rustling
grass - so those labels are what this model reaches for when it is out of its
depth, not a cat problem.

## What the recordings actually contain

Measured with `cmd/clipspectrum`. Every clip carries an unmistakable aircraft
signature, so nothing was wrong with the capture:

| Clip | Below 250 Hz | Below 500 Hz | Peak | RMS |
|---|---|---|---|---|
| helicopter | 54.5% | 69.6% | -21.7 dBFS | -38.7 dBFS |
| jet | 40.8% | 62.5% | -27.0 dBFS | -41.7 dBFS |
| jet, high | 40.8% | 65.4% | -26.4 dBFS | -42.8 dBFS |
| propeller | 32.2% | 53.6% | -19.4 dBFS | -35.7 dBFS |

**The recordings are very quiet** - around -40 dBFS RMS. YAMNet was trained on
normal-loudness audio, and that turns out to matter more than anything else.

## What preprocessing does

Measured with `cmd/clipscore`. The `Aircraft` class score:

| Clip (truth) | raw | normalised | **LPF 1.2k + norm** | LPF 600 + norm |
|---|---|---|---|---|
| propeller | 0.016 | 0.082 | 0.109 | 0.148 |
| propeller | 0.031 | 0.082 | 0.109 | 0.031 |
| aircraft | 0.148 | 0.262 | **0.500** | 0.414 |
| commercial jet | **0.000** | 0.043 | **0.332** | 0.148 |
| jet, high | **0.000** | - | 0.199 | - |
| helicopter | **0.000** | 0.012 | 0.059 | 0.082 |

Three of six were at **exactly zero** on the raw audio. They were not missed by
a threshold; they were invisible to the model. Low-passing at 1.2 kHz and then
normalising makes all six non-zero and two of them usable.

**The order matters.** Low-pass first, normalise second. Peak normalisation on
the raw clip is hostage to the loudest transient - one close bird chirp sets the
peak and the aircraft under it is barely lifted. After the low-pass the peak is
the aircraft, so normalising actually helps. RMS normalisation to 0.15 performs
about the same as peak 0.9; RMS 0.05 is worse than either.

## Operator review notes, and what they corroborate

Three clips were reviewed in the web UI with free-text notes. The notes are
worth more than the false-positive flags, because they describe how clearly the
operator themselves could hear the aircraft - and the model's scores track that
judgement:

| Clip | Operator's note | raw | LPF+norm | `Vehicle` |
|---|---|---|---|---|
| `cat_85p_...105143Z` | "Loud and clear propeller aircraft passing overhead" | 0.148 | **0.500** | 0.586 |
| `cat_74p_...104608Z` | "High flying jet passing overhead, distant human speech, loud crow" | 0.000 | 0.148 | 0.738 |
| `cat_85p_...105425Z` | "High passing aircraft **or distant traffic noise**, close field grass rustling or sizzling BBQ" | 0.000 | 0.109 | 0.738 |

The confidence is not arbitrary: it falls as the operator's own certainty falls.
On the third clip the operator could not tell aircraft from traffic either, and
the model's answer - `Vehicle` 0.738, `Aircraft` 0.109 - is arguably the honest
one rather than a mistake.

### `Vehicle` is the reliable signal, not `Aircraft`

After preprocessing, `Vehicle` scores **0.59 to 0.74 on every aircraft clip** -
higher than `Aircraft` in all three, and far more stable. The model detects
"something motorised" dependably and attributes it to the wrong kind of vehicle.

This is the single most useful result in the set, because it says where the
acoustic layer should stop:

- **Acoustics answer "is something motorised nearby?"** - reliably, at 0.6-0.74.
- **Acoustics cannot answer "aircraft or lorry?"** - and neither could the
  operator on one of the three.
- **ADS-B answers it** - an aircraft at a credible slant range makes it an
  aircraft; nothing overhead makes it road traffic.

**Consequence for the pipeline.** `Domain.Enrichable()` is currently true only
for aircraft and weather, so a detection classified `DomainVehicle` never
reaches the ADS-B provider - and on this evidence that is precisely the case
that needs it most. Enrichment has to be attempted for the vehicle domain too,
with a confirmed overflight promoting the detection to aircraft.

## What this means for the design

**Presence is recoverable; type is not.** On the helicopter clip, `Helicopter`
scored 0.012 while `Fixed-wing aircraft, airplane` scored 0.043 - the model gets
*aircraft* roughly right and the *kind* wrong. This vindicates the scope's
insistence on separating the layers: take the class from acoustics, take the
identity and type from ADS-B, and never let one pretend to be the other.

**ADS-B corroboration is essential, not decorative.** A 0.15 acoustic score will
never clear a sensible threshold alone. The same 0.15 alongside a confirmed
aircraft at a credible slant range is a confident detection. M5 was scoped as
enrichment; it is actually load-bearing.

**Do not trust a confident YAMNet label at this SNR.** It called a jet `Cat` at
0.85. The taxonomy filter already suppresses those classes, which is the filter
earning its place - but the lesson generalises to the classes we do keep.

**Raising the capture gain is the largest untapped lever.** At -40 dBFS RMS
every downstream stage is fighting the input. `realtime.audio.sources[].gain` is
currently 9.
