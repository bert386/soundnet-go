# SCOPE — General Acoustic Event Detection Platform (working name: **Acuity**)

A build brief for Claude Code. A **private, non-commercial fork** of BirdNET-Go, extended
from a bird-only soundscape analyser into a general acoustic event detector with a
diagnostics layer (near/far, speed, count, duration), sub-classification heads (aircraft
type, saw type, etc.), external enrichment (ADS-B, lightning/weather), and a web UI for
verifying and refining classification.

---

## 0. License (non-blocking, but observe)

Upstream `tphakala/birdnet-go` is **CC BY-NC-SA 4.0**. This fork is **personal and
non-commercial**, which the license permits. Obligations to keep:
- **Attribution:** retain upstream `LICENSE`, add a `NOTICE` crediting BirdNET-Go / Tomi P. Hakala.
- **ShareAlike:** if the fork is ever published, it stays CC BY-NC-SA 4.0.
- **NonCommercial:** no selling, no hosting-as-a-service, no bundling into a paid product. (Not legal advice.)

Everything below is unconstrained by this.

---

## 1. Upstream architecture (ground truth for the agent)

- **Backend:** Go, Echo v4 (`labstack/echo/v4`).
- **Inference:** `github.com/tphakala/go-tflite` (TFLite). Models embedded or loaded from a gallery.
- **Audio capture:** `github.com/tphakala/malgo` — soundcard + RTSP/RTSPS, multi-source parallel.
- **DB:** `gorm.io/gorm`, SQLite default (`birdnet.db`), MySQL optional. Detection record = `Note` struct (`internal/datastore/model.go`).
- **Config:** Viper, `config.yaml` (`internal/conf/`).
- **Frontend:** Svelte 5 + TypeScript + Tailwind, HTTP/SSE (`frontend/src`). Legacy HTMX/Alpine views being retired — **build new UI in Svelte 5 only**.
- **Existing, reuse directly:**
  - Multi-model gallery + parallel inference + cross-model consensus.
  - **Custom TFLite classifiers + append mode** (`wiki/Training-a-Custom-Classifier`).
  - **Sound level monitoring in 1/3-octave bands** with MQTT/SSE/Prometheus — foundation for level/near-far diagnostics.
  - Alert rules engine → MQTT (HA discovery), webhooks, ntfy, shell, Discord/Slack/Telegram.
  - Live spectrogram streaming; detection heatmaps.
- **Dev workflow:** `task setup-dev`, `air realtime` (hot reload), git hooks, automated quality gates.
- **Perf target:** RPi 3B+ processes a 3 s segment in ~500 ms. Coral TPU unsupported (model incompatibility) — keep added models CPU-cheap. YAMNet (~3.7M params) is fine.

---

## 2. Product vision (one paragraph)

Detection is three stacked problems and the platform must treat them separately:
**(1) class** — what kind of thing (off-the-shelf/custom models, coarse labels);
**(2) properties** — near/far, speed, count, duration, computed by DSP on the detection clip, *not* labels;
**(3) identity** — the exact thing, resolved from an authoritative external source (ADS-B for aircraft, lightning network for thunder). The UI exists to let a human verify and refine (1), tune (2), and confirm (3), and to turn confirmed detections into training data.

---

## 3. Target event taxonomy (initial)

Model layer emits coarse class; diagnostics/enrichment produce the rest.

| Event | Class source | Diagnostics (layer 2) | Enrichment (layer 3) | Feasible single-mic |
|---|---|---|---|---|
| Aircraft present | YAMNet/custom | level, duration | ADS-B | Yes |
| Aircraft type (jet/prop/heli) | custom head | — | ADS-B confirms | Yes |
| Aircraft exact flight/type | — | — | **ADS-B (authoritative)** | via ADS-B |
| Car / motorbike / truck pass-by | class | **Doppler → speed + CPA**, level, spectral tilt | — | Yes |
| Truck near vs far | class | level + HF spectral tilt | — | relative only |
| Gunshot | class | **onset count** (refractory gate) | — | Yes (count) |
| Gunshot vs backfire | class | impulse shape features | — | **hard — flag as low-confidence** |
| Thunderclap near/far + duration | class | spectral tilt + envelope length | lightning net + weather | Yes |
| Hammer / nail gun / circular saw / hand saw | class + texture | onset rate, continuity, tonal-whine detect | — | Yes |
| Other natural/manmade | YAMNet 521 classes | per-class as configured | pluggable | varies |

Non-goals for v1 (document, don't build): direction-of-arrival / bearing and source
separation of simultaneous events — these need a 2+ mic array. Leave clean seams for a
future array front-end.

---

## 4. Milestones

Each milestone: deliverables, files, acceptance criteria. Ship in order; each is a PR. All
new code lives in new `internal/*` packages; touch upstream files minimally (thin hooks
only), one clearly-marked integration point per pipeline stage, so upstream stays mergeable.

### M0 — Project setup
- Fork; rename module/binary; add `LICENSE` (retained) and `NOTICE` (attribution).
- CI: build, `go test`, lint, frontend build. Reproduce `task setup-dev` locally.
- **Accept:** clean build + existing tests green; binary runs; UI loads.

### M1 — Generalise the model/taxonomy layer
Decouple bird-specific assumptions so non-bird label sets are first-class.
- Add **YAMNet** as a gallery model (TFLite; input framing 16 kHz mono, 0.975 s / 15600 samples → 521 AudioSet scores). Map AudioSet ontology to internal event classes.
- Abstract label handling: species → generic `EventClass {id, domain, label, parent}`; keep bird taxonomy as one domain.
- UI copy/labels de-birded behind the existing i18n layer.
- **Accept:** YAMNet detections land in the DB with correct class labels alongside/instead of bird models; gallery install works without rebuild.

### M2 — Detection record extension + migration
- Add to the `Note` model:
  `Diagnostics JSON` (computed properties), `Enrichment JSON` (external identity),
  `ReviewState enum {unreviewed, confirmed, corrected, false_positive}`,
  `CorrectedClassID nullable`, `ClipPath` (retain audio for review/training).
- GORM auto-migration + backfill defaults. Index `ReviewState`, `class`, `date`.
- **Accept:** migration idempotent on existing DB; new fields read/write via datastore interface.

### M3 — DSP diagnostics engine (Go) — *the properties layer*
New package `internal/diagnostics`. Runs on the detection clip after class is assigned;
writes `Diagnostics` JSON. Deterministic, unit-tested, RPi-cheap.
- **Level / near-far:** reuse existing 1/3-octave band data; compute broadband SPL proxy + **HF spectral tilt** (air absorbs high frequencies with distance) as a relative near/far indicator. Absolute distance only with a calibrated-mic config flag.
- **Transient onset counter:** peak-pick with adaptive threshold + refractory period. Powers gunshot count, hammer blows, nail-gun reports. Emits `count`, `interval_stats`.
- **Envelope/duration:** onset→decay length. Powers thunder near/far + duration.
- **Doppler pass-by / CPA:** track dominant-frequency shift across the clip; fit the Doppler curve to estimate **speed** and **closest-point-of-approach distance**. Powers car/truck/motorbike near/far + speed. (No bearing — single mic.)
- **Impulse-shape features:** for gunshot-vs-backfire, extract crest factor, rise time, spectral centroid; expose as features + a **low-confidence flag**, do not assert.
- Config: per-class enable + params in `config.yaml` under `diagnostics:`.
- **Accept:** golden-file tests on labelled clips (provide fixtures) within tolerance; runs <100 ms/clip on RPi 4.

### M4 — Sub-classification heads
Small TFLite heads on YAMNet 1024-D embeddings, plugged into the gallery via append-mode.
- Aircraft **jet / prop / helicopter** (blade-passage harmonics vs broadband roar vs rotor slap).
- Saw **circular vs hand** (continuous broadband+tonal whine vs ~1 Hz rhythmic strokes).
- Training pipeline stub: `embeddings → shallow classifier → TFLite export` (documented, scripted). Aircraft training data comes from M6.
- **Accept:** heads load in gallery; inference path produces subtype label with confidence; documented retrain command.

### M5 — Enrichment layer — *the identity layer*
New package `internal/enrichment`. **Provider interface** keyed by event class; each provider
takes `{class, timestamp, confidence, station_geo}` and returns a structured identity written
to `Enrichment` JSON.
- **ADS-B provider (priority):**
  - Sources (configurable, in priority order): local `dump1090`/`readsb` `aircraft.json` (tar1090) → OpenSky REST → airplanes.live / adsb.fi feed. Local first for latency + raw data.
  - **Acoustic-lag correction:** back-date detection time by `slant_range / 343 m·s⁻¹`, `slant_range = √(horizontal² + altitude²)`; select nearest/lowest track in a window around the corrected time. Usually one aircraft is audible, so match is unambiguous.
  - Resolve `hex → type/registration` (decoder aircraft DB / basestation.sqb) and `callsign → route` (route API).
  - Output: `{callsign, hex, type, registration, altitude, cpa_km, source}`.
- **Lightning/weather provider:** thunder → Blitzortung feed + weather API for corroboration/distance sanity-check.
- Providers for siren/gunshot/vehicle: none public — return `null`, don't fabricate.
- Config under `enrichment:` with per-provider creds/endpoints.
- **Human input needed:** station lat/lon/elevation; chosen ADS-B source. Surface as required config with validation, not hard-coded.
- **Accept:** given a recorded aircraft detection + a captured `aircraft.json` snapshot fixture, provider returns the correct flight; lag correction unit-tested against known geometry.

### M6 — ADS-B auto-labeling harness (the training flywheel)
Reverse of runtime: when ADS-B shows an aircraft overhead + low, trigger a clip capture and
label it with type/registration/altitude/CPA (this is how the AeroSonicDB dataset was built).
- Runs as an optional background collector; writes typed clips to a training corpus dir.
- Feeds M4 aircraft-type head; accumulates a site-specific dataset over time.
- **Accept:** overflight produces a correctly-named, correctly-tagged clip; corpus browsable in UI (M7).

### M7 — Web UI: diagnostics + refinement tools *(the explicit ask)*
Svelte 5 + TS + Tailwind, new routes/components. This is where classification gets refined.
- **Detection detail panel:** spectrogram + waveform + computed diagnostics (level/tilt, count, duration, Doppler curve + speed/CPA, impulse features) + enrichment card (flight/type). Audio playback.
- **Review queue:** filter `unreviewed`; per detection **Confirm / Correct class / Mark false-positive**; keyboard-driven for speed. Writes `ReviewState` + `CorrectedClassID`.
- **Confusion review:** paired view for known-hard confusions (gunshot vs backfire) to build a labelled discrimination set.
- **Per-class threshold tuner:** live-adjust detection + diagnostics thresholds; preview effect on recent detections before saving to `config.yaml` (hot-reload).
- **Training export:** turn confirmed/corrected clips into a labelled dataset (folder or manifest) for M4 retrains; show corpus stats incl. M6 aircraft clips.
- **Live diagnostics tuning:** adjust onset refractory, Doppler fit window, tilt bands with immediate re-compute on a selected clip.
- **Accept:** operator can go unreviewed → confirmed/corrected in the queue; a correction updates the record and appears in the training export; threshold changes persist and hot-reload.

### M8 — Enriched alerts / integration
Carry `Diagnostics` + `Enrichment` through the existing alert engine.
- Templated payloads, e.g. `Aircraft: QFA123 · A320 · 4000 ft · CPA 1.2 km`,
  `Gunshots: 3 · impulse (low-confidence: possible backfire)`,
  `Truck pass-by · ~72 km/h · CPA ~40 m`.
- MQTT (HA discovery entities per class), webhook, ntfy.
- **Accept:** a detection with enrichment emits the templated message on the configured channel.

---

## 5. Cross-cutting requirements
- **Upstream-mergeable:** all new code in new `internal/*` packages; thin hooks into upstream; one integration point per pipeline stage.
- **Config:** all new behaviour behind `config.yaml` keys, defaults off where it adds cost. Validate on load.
- **Privacy-by-design:** match upstream — no external calls without explicit opt-in; ADS-B/weather providers opt-in and clearly disclosed.
- **Perf budget:** diagnostics + enrichment must not break real-time on RPi 4; each stage skippable per class. Benchmark in CI.
- **Testing:** unit tests for every DSP function (golden files), migration tests, provider tests against fixture snapshots. No network in tests.
- **Observability:** extend existing Prometheus metrics with per-stage timing + per-class detection/enrichment counts.

## 6. Open decisions for the human (Claude Code: ask, don't assume)
1. ADS-B source: local `dump1090`/`readsb` vs OpenSky vs airplanes.live/adsb.fi.
2. Station geo: lat / lon / elevation (required for M5/M6).
3. Target hardware for deployment (RPi 4 / x86) — sets the perf budget.
4. Final project name (replace **Acuity** placeholder).

## 7. First PR
M0 + M1 skeleton: build under the new name, YAMNet installed in the gallery producing
general-class detections into the DB, existing tests green. Everything else stacks on that.
