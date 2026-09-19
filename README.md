# SoundNet

<p align="center">
  <a href="https://creativecommons.org/licenses/by-nc-sa/4.0/">
    <img src="https://badgen.net/badge/License/CC-BY-NC-SA%204.0/green">
  </a>
  <a href="https://golang.org">
    <img src="https://img.shields.io/badge/Built%20with-Go-teal?style=flat-square&logo=go">
  </a>
  <img src="https://badgen.net/badge/Status/in%20development/orange">
  <img src="https://badgen.net/badge/Target/Raspberry%20Pi%204/blue">
</p>

**A general acoustic event detector with a diagnostics and identity layer.**

SoundNet is a private, non-commercial fork of
[BirdNET-Go](https://github.com/tphakala/birdnet-go) by Tomi P. Hakala, extended from a
bird-focused soundscape analyser into a general acoustic event detection platform.

## Why this exists

Detection is not one problem. It is three, and conflating them is what makes acoustic
classifiers disappointing in practice. SoundNet treats them separately:

| Layer | Question | How it is answered |
|---|---|---|
| **1. Class** | *What kind of thing is that?* | Off-the-shelf and custom models emitting coarse labels — aircraft, gunshot, saw, thunder. |
| **2. Properties** | *What was it doing?* | DSP measurements computed on the detection clip: near/far, speed, count, duration. **Measurements, not labels.** |
| **3. Identity** | *Which exact one was it?* | Resolved from an authoritative external source — ADS-B for aircraft, lightning networks for thunder. |

The distinction matters because each layer fails differently. A model can tell you "aircraft"
but never the registration. DSP can tell you a vehicle passed at roughly 70 km/h but not what
make it was. And for a siren or a gunshot there is **no** authoritative source, so the identity
layer returns nothing rather than inventing something.

The web UI exists so a human can verify layer 1, tune layer 2, confirm layer 3 — and turn
confirmed detections back into training data.

## What it detects

Aircraft (with type, and exact flight via ADS-B), vehicle pass-bys with estimated speed and
closest-point-of-approach, gunshots with onset counting, thunder with distance and duration,
power tools by texture, and the general AudioSet ontology via YAMNet.

Some things are honestly hard. Gunshot versus vehicle backfire is not reliably separable from a
single microphone, so SoundNet extracts impulse-shape features and **flags the result as
low-confidence rather than asserting an answer**.

## What it deliberately does not do

Direction-of-arrival and separating simultaneous overlapping sources both need a 2+ microphone
array. Neither is in scope for v1. The audio front-end keeps clean seams for a future array.

## Status

Under active development, built in ordered milestones. See
[doc/soundnet/SCOPE.md](doc/soundnet/SCOPE.md) for the full brief and
[doc/soundnet/DECISIONS.md](doc/soundnet/DECISIONS.md) for the decisions log.

| | Milestone | State |
|---|---|---|
| M0 | Project setup, fork rename, attribution, CI | in progress |
| M1 | Generalise the model/taxonomy layer, add YAMNet | |
| M2 | Detection record extension + migration | |
| M3 | DSP diagnostics engine (the properties layer) | |
| M4 | Sub-classification heads (aircraft type, saw type) | |
| M5 | Enrichment layer (the identity layer; ADS-B, lightning) | |
| M6 | ADS-B auto-labelling harness (the training flywheel) | |
| M7 | Web UI: diagnostics, review queue, threshold tuning | |
| M8 | Enriched alerts and integrations | |

## Installation

**There are no published releases, binaries or container images for SoundNet, by design** — it
is a personal, non-commercial project. Build it from source:

```bash
task setup-dev    # installs the TFLite C library, Go tools and frontend deps
task              # build for the host platform
```

A Linux environment is expected; the deployment target is a Raspberry Pi 4. Where the inherited
documentation under `doc/wiki/` refers to installers, releases or `ghcr.io` images, those belong
to **upstream BirdNET-Go**, not to this fork.

## Inherited capability

SoundNet builds directly on upstream's foundations rather than reimplementing them: the
multi-model gallery with parallel inference and cross-model consensus, custom TFLite classifier
support, 1/3-octave sound level monitoring (which the near/far diagnostics build on), the alert
rules engine with MQTT/webhook/ntfy routing, and live spectrogram streaming.

## Privacy

Local-only by default, matching upstream. The external enrichment providers (ADS-B, weather)
are **opt-in**, disabled unless configured, and documented as making outbound calls. Station
coordinates are operator-entered and never leave the machine except as the bounding box of an
ADS-B query the operator has enabled.

## Licence and attribution

CC BY-NC-SA 4.0, inherited from BirdNET-Go and retained unchanged. This fork is personal and
non-commercial: not sold, not hosted as a service, not bundled into any paid product. If it is
ever published it stays under the same licence.

Full attribution, including the BirdNET model and label translation credits, is in
[NOTICE](NOTICE) and [AUTHORS](AUTHORS).
