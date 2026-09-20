"""Does preferring the specific child class over AudioSet's parent help?

The operator asked why aircraft are recorded as vehicles. `Vehicle` is the
parent class of `Aircraft` in AudioSet, so it scores higher for the same sound
and wins the label. The obvious fix is to prefer the child when both fire - but
"obvious" is how the low-pass normalisation went wrong, which looked excellent
until it was swept across the clips that contain no aircraft at all.

So this scores both the positives and the negatives, and reports what any
proposed rule costs on the negatives as prominently as what it wins on the
positives.

Uses the fused single-window export, which is the model the station actually
runs, rather than the sherpa oracle, whose kaldi front-end is an approximation
of it. Windows slide across the whole clip because that is what the station
does; a middle-window score would flatter a rule that only needs to be right
once.
"""
import json
import os
import sys
import wave

import numpy as np
import onnxruntime as ort
import torch
import torchaudio

MODELS = "/root/gotmp/models"
CED = f"{MODELS}/sherpa-onnx-ced-tiny-audio-tagging-2024-04-19"
SINGLE = f"{MODELS}/ced_tiny_fused_single.onnx"
CLIPS = "/root/gotmp/clips"

WINDOW = 48000  # 3 s at 16 kHz, the station's analysis window
HOP = 24000     # 1.5 s, the station's overlap

# What the operator said, keyed by detection id.
TRUTH = {
    674: "aircraft", 679: "aircraft", 686: "aircraft", 711: "aircraft",
    715: "aircraft", 726: "aircraft", 730: "aircraft", 733: "aircraft",
    759: "aircraft", 805: "aircraft", 824: "aircraft", 828: "aircraft",
    905: "vehicle",
    817: "other", 720: "other", 693: "other", 702: "other", 725: "other",
    838: "other", 880: "other", 929: "other", 932: "other", 937: "other",
    826: "other",
}

# Clips that were not in the snapshot and were fetched from the station by hand.
EXTRA_CLIPS = {
    674: "chirp_59p_20260920T105104Z.wav",
    679: "chirp_50p_20260920T105117Z.wav",
    686: "cat_85p_20260920T105143Z.wav",
    693: "chirp_59p_20260920T105234Z.wav",
    702: "chicken_50p_20260920T105304Z.wav",
    711: "chirp_50p_20260920T105328Z.wav",
    715: "chicken_50p_20260920T105343Z.wav",
}

AIRCRAFT = {
    "Aircraft", "Aircraft engine", "Jet engine",
    "Propeller, airscrew", "Helicopter", "Fixed-wing aircraft, airplane",
}
PARENT = "Vehicle"

labels = []
with open(f"{CED}/class_labels_indices.csv") as fh:
    next(fh)
    for line in fh:
        labels.append(line.rstrip("\n").split(",", 2)[2].strip().strip('"'))
index_of = {name: i for i, name in enumerate(labels)}

for name in AIRCRAFT | {PARENT}:
    if name not in index_of:
        sys.exit(f"label {name!r} is not in the CED class map")

sess = ort.InferenceSession(SINGLE, providers=["CPUExecutionProvider"])
inp = sess.get_inputs()[0].name


def read_mono_16k(path):
    with wave.open(path, "rb") as w:
        n, ch, sr = w.getnframes(), w.getnchannels(), w.getframerate()
        raw = w.readframes(n)
    a = np.frombuffer(raw[: (len(raw) // 2) * 2], dtype="<i2").astype(np.float32) / 32768.0
    if ch > 1:
        a = a.reshape(-1, ch).mean(axis=1)
    wav = torch.from_numpy(a.copy())
    if sr != 16000:
        wav = torchaudio.functional.resample(wav, sr, 16000)
    return wav


def per_class_max(path):
    """Highest score each class reaches over any window of the clip."""
    wav = read_mono_16k(path)
    if wav.numel() < WINDOW:
        wav = torch.nn.functional.pad(wav, (0, WINDOW - wav.numel()))
    best = None
    for start in range(0, max(1, wav.numel() - WINDOW + 1), HOP):
        win = wav[start:start + WINDOW]
        if win.numel() < WINDOW:
            break
        out = sess.run(None, {inp: win.unsqueeze(0).numpy().astype(np.float32)})[0][0]
        best = out if best is None else np.maximum(best, out)
    return best


def clip_paths():
    rows = json.load(open("/root/gotmp/dets.json"))["data"]
    by_id = {r["id"]: os.path.basename(r.get("clipName") or "") for r in rows}
    out = {}
    for det_id in TRUTH:
        name = EXTRA_CLIPS.get(det_id) or by_id.get(det_id, "")
        path = os.path.join(CLIPS, name) if name else ""
        if path and os.path.exists(path):
            out[det_id] = path
    return out


paths = clip_paths()
missing = sorted(set(TRUTH) - set(paths))
if missing:
    print(f"no clip for: {missing}\n")

scores = {}
for det_id in sorted(paths):
    vec = per_class_max(paths[det_id])
    parent = float(vec[index_of[PARENT]])
    child_name, child = max(
        ((name, float(vec[index_of[name]])) for name in AIRCRAFT), key=lambda kv: kv[1]
    )
    top_i = int(np.argmax(vec))
    scores[det_id] = {
        "truth": TRUTH[det_id],
        "parent": parent,
        "child": child,
        "child_name": child_name,
        "top": labels[top_i],
        "top_score": float(vec[top_i]),
    }

print(f"{'id':>5} {'truth':<9} {'Vehicle':>8} {'best aircraft':>14} {'class':<28} {'top-1':<24}")
print("-" * 95)
for det_id, s in sorted(scores.items(), key=lambda kv: (kv[1]["truth"], -kv[1]["child"])):
    print(f"{det_id:>5} {s['truth']:<9} {s['parent']:>8.3f} {s['child']:>14.3f} "
          f"{s['child_name']:<28} {s['top'][:22]:<24}")

positives = [s for s in scores.values() if s["truth"] == "aircraft"]
negatives = [s for s in scores.values() if s["truth"] != "aircraft"]
print(f"\n{len(positives)} aircraft, {len(negatives)} not-aircraft\n")

print("Rule A - call it aircraft when the best aircraft class clears an absolute bar")
print(f"{'bar':>6} {'caught':>8} {'of':>4} {'false':>7} {'of':>4}")
for bar in [0.02, 0.05, 0.08, 0.10, 0.15, 0.20, 0.25, 0.30, 0.40, 0.50]:
    tp = sum(1 for s in positives if s["child"] >= bar)
    fp = sum(1 for s in negatives if s["child"] >= bar)
    print(f"{bar:>6.2f} {tp:>8} {len(positives):>4} {fp:>7} {len(negatives):>4}")

print("\nRule B - prefer the child over Vehicle when it is at least this fraction of it")
print(f"{'ratio':>6} {'caught':>8} {'of':>4} {'false':>7} {'of':>4}")
for ratio in [0.10, 0.15, 0.20, 0.25, 0.33, 0.50, 0.75, 1.00]:
    tp = sum(1 for s in positives if s["parent"] > 0 and s["child"] >= ratio * s["parent"])
    fp = sum(1 for s in negatives if s["parent"] > 0 and s["child"] >= ratio * s["parent"])
    print(f"{ratio:>6.2f} {tp:>8} {len(positives):>4} {fp:>7} {len(negatives):>4}")

print("\nRule C - Rule B, but only once Vehicle itself is worth believing")
print(f"{'ratio':>6} {'vehBar':>7} {'caught':>8} {'of':>4} {'false':>7} {'of':>4}")
for veh_bar in [0.10, 0.20, 0.30]:
    for ratio in [0.15, 0.25, 0.33, 0.50]:
        tp = sum(1 for s in positives
                 if s["parent"] >= veh_bar and s["child"] >= ratio * s["parent"])
        fp = sum(1 for s in negatives
                 if s["parent"] >= veh_bar and s["child"] >= ratio * s["parent"])
        print(f"{ratio:>6.2f} {veh_bar:>7.2f} {tp:>8} {len(positives):>4} {fp:>7} {len(negatives):>4}")

with open("/root/gotmp/hierarchy_scores.json", "w") as fh:
    json.dump(scores, fh, indent=1)
print("\nwrote /root/gotmp/hierarchy_scores.json")
