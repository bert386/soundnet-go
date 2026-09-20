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

**Consequence for the pipeline, now implemented.** `Domain.Enrichable()` was
true only for aircraft and weather, so a detection classified `DomainVehicle`
never reached the ADS-B provider - precisely the case that needs it most.
`Vehicle` and `Engine` now carry candidate domains and the pipeline asks each
authority in turn; see `internal/eventclass/ambiguity.go`. The domain itself is
unchanged and still not enrichable, because a detection that really is a car
still has no authority that can name it.


## The full annotated set, 2026-09-20

The operator later reviewed and annotated every non-bird detection of the
morning, and verified each one correct or false-positive in the UI. Twenty-four
rows, which is the largest labelled set the project has. It is recorded in full
because the aggregate says something none of the individual clips does.

**Twelve of the twenty-four are aircraft. Two were labelled as aircraft.**

| id | YAMNet said | conf | operator |
|---|---|---|---|
| 674 | Chirp tone | 0.59 | two propeller aircraft, two tones |
| 679 | Chirp tone | 0.50 | propeller aircraft |
| 686 | **Cat** | 0.85 | loud, clear propeller overhead |
| 711 | Chirp tone | 0.50 | distant jet; car-unlock chirp at the start |
| 715 | Chicken, rooster | 0.50 | distant commercial jet |
| 726 | **Cat** | 0.85 | high aircraft or distant traffic |
| 730 | Chicken, rooster | 0.59 | distant aircraft, probably propeller |
| 733 | Chirp tone | 0.50 | distant propeller **and** approaching helicopter |
| 759 | **Vehicle** | 0.80 | propeller aircraft overhead |
| 805 | Propeller, airscrew | 0.41 | turbo-prop passing - **correct** |
| 824 | **Thunderstorm** | 0.80 | commercial jet |
| 828 | Propeller, airscrew | 0.50 | close propeller aircraft - **correct** |

And the rest:

| id | YAMNet said | conf | operator |
|---|---|---|---|
| 905 | Vehicle | 0.80 | large truck, manual transmission - **correct** |
| 817 | Police car (siren) | 0.74 | human whistling |
| 693, 720 | Chirp tone | 0.59 | hammering, moving furniture |
| 702 | Chicken, rooster | 0.50 | hammering, tapping, faint speech |
| 725 | Chicken, rooster | 0.50 | rustling grass |
| 838, 929, 932, 937 | Chewing, mastication | 0.50-0.59 | rustling dry grass, hedge, sticks |
| 880 | Crying, sobbing | 0.41 | a crow |
| 826 | *Willie-wagtail* (BirdNET) | 0.71 | no thunder - distant mower or traffic |

### `Vehicle` 0.80 is a truck and `Vehicle` 0.80 is an aeroplane

This is the sharpest evidence in the set, and it needs no analysis: id 905 and
id 759 carry the **same label at the same score** and are a lorry and an
aircraft. The acoustic layer is not being imprecise here - it is answering
correctly and completely, and the question it answers is "is something motorised
nearby". Nothing in the audio distinguishes the two at this SNR. Only ADS-B can,
which is why id 759 is the case the domain-ambiguity work exists for.

### Thunderstorm 0.80 on a commercial jet

Not in the ontology's hierarchy the way `Vehicle` is, but the confusion is
physical: a jet and distant thunder are both low-frequency broadband with a slow
envelope. Two consequences.

`ConfusionSet` should pair them - it already pairs Thunder with Explosion, and
this is the same kind of neighbour. More interesting, `DomainWeather` **is**
enrichable, but only a lightning provider would ever be registered for it, so a
jet recorded as Thunderstorm reaches an authority that cannot help and never
reaches the one that can. Adding aircraft as a candidate domain for the thunder
classes would fix that with the machinery already built.

It is deliberately **not** done yet. `Vehicle` and `Engine` earned their place
by being AudioSet superclasses that genuinely contain aircraft - an argument
from the ontology that holds regardless of this station. Thunder would be there
on the strength of one clip, and a thunderstorm produces a great many detections
to spend credits on. Decide it with more evidence, not less.

### The mislabelled aircraft would now be silent

`Chirp tone`, `Cat` and `Chicken, rooster` are not in the event taxonomy, so the
current build does not emit them at all: eight of the twelve aircraft above
would today produce **no detection whatsoever** rather than a wrong one. That is
the taxonomy filter working exactly as designed, and it is worth being clear
that it makes the list cleaner without making the station better at hearing
aircraft.

So the filter is not the lever. The levers are the ones that change what the
model is given: raise the capture gain from 9, and run the low-frequency second
pass. Both act on the -40 dBFS input rather than on the labels.

### Three labels that are not what they say

`Chewing, mastication` at 0.50-0.59 is rustling dry grass, four times over. It
is emitted only as an input to the privacy filter and has no business being
stored as a detection - see STATE.md. `Police car (siren)` 0.74 is a person
whistling. `Crying, sobbing` 0.41 is a crow. All three are human- or
animal-vocal classes firing on textures, at scores that clear a 0.7-ish bar
without meaning anything.

And once in the other direction: id 826 is **BirdNET** calling a Willie-wagtail
at 0.71 on what the operator hears as a distant mower or passing traffic. Bird
classification is not immune to machinery either.

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
