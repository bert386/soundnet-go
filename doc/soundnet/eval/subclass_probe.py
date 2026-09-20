"""Can the model tell a bin truck from a car, or a lawn mower from either?

The operator wants `Vehicle` broken into car / truck / motorbike, `Aircraft`
into jet / propeller / helicopter, and a tools category. The taxonomy already
has every one of those classes and `DomainTool` already exists - so the question
is not whether we can name them, it is whether the model says anything useful
when it hears one. A category the model never populates is a filter that always
returns nothing.

Scored with the fused CED export the station runs, window by window, taking each
class's best window. Ground truth is the operator's own note on each clip.
"""
import glob
import json
import os
import wave

import numpy as np
import onnxruntime as ort
import torch
import torchaudio

MODELS = "/root/gotmp/models"
CED = f"{MODELS}/sherpa-onnx-ced-tiny-audio-tagging-2024-04-19"
SINGLE = f"{MODELS}/ced_tiny_fused_single.onnx"
CLIPS = "/root/gotmp/labelled"
WINDOW, HOP = 48000, 24000

# The operator's note, keyed by clip.
TRUTH = {
    "vehicle_74p_20260920T172858Z.wav": "drums + lawnmower",
    "vehicle_80p_20260920T173300Z.wav": "lawnmower + drums",
    "vehicle_74p_20260920T173624Z.wav": "lawnmower",
    "vehicle_74p_20260920T173709Z.wav": "lawnmower",
    "vehicle_85p_20260920T174209Z.wav": "drums",
    "vehicle_41p_20260920T174747Z.wav": "drums (full kit)",
    "vehicle_41p_20260920T174811Z.wav": "drums + distant jet",
    "vehicle_33p_20260920T170710Z.wav": "prop plane",
    "vehicle_73p_20260921T065018Z.wav": "bin truck, very close",
    "vehicle_77p_20260921T070203Z.wav": "roadworks beeper",
    "vehicle_77p_20260921T072917Z.wav": "car + roadworks",
    "vehicle_72p_20260921T073611Z.wav": "plant machinery + beeper",
    "vehicle_72p_20260921T073656Z.wav": "plant machinery",
    "vehicle_85p_20260921T080300Z.wav": "bin truck, close",
    "vehicle_76p_20260921T080324Z.wav": "bin truck, distant",
    "vehicle_74p_20260921T081935Z.wav": "bin truck, distant",
    "vehicle_80p_20260921T082006Z.wav": "bin truck approaching",
    "vehicle_85p_20260921T082033Z.wav": "bin truck, close",
    "vehicle_70p_20260921T082103Z.wav": "bin truck accelerating",
    "vehicle_70p_20260921T082551Z.wav": "passing car",
    "vehicle_72p_20260921T082617Z.wav": "passing car",
}

# The classes the operator asked for, plus the parent each competes with.
WATCH = [
    "Vehicle", "Motor vehicle (road)",
    "Car", "Car passing by", "Truck", "Bus", "Motorcycle", "Air brake",
    "Engine", "Medium engine (mid frequency)", "Heavy engine (low frequency)",
    "Lawn mower", "Chainsaw", "Power tool", "Drill", "Hammer", "Jackhammer", "Sawing",
    "Tools", "Reversing beeps",
    "Aircraft", "Jet engine", "Propeller, airscrew", "Helicopter",
    "Fixed-wing aircraft, airplane",
    "Drum kit", "Drum", "Cymbal", "Snare drum", "Music", "Percussion",
]

labels = []
with open(f"{CED}/class_labels_indices.csv") as fh:
    next(fh)
    for line in fh:
        labels.append(line.rstrip("\n").split(",", 2)[2].strip().strip('"'))
idx = {name: i for i, name in enumerate(labels)}
missing = [w for w in WATCH if w not in idx]
if missing:
    print(f"not in the CED class map: {missing}\n")
watch = [w for w in WATCH if w in idx]

sess = ort.InferenceSession(SINGLE, providers=["CPUExecutionProvider"])
inp = sess.get_inputs()[0].name


def mono16k(path):
    with wave.open(path, "rb") as w:
        n, ch, sr = w.getnframes(), w.getnchannels(), w.getframerate()
        raw = w.readframes(n)
    a = np.frombuffer(raw[: (len(raw) // 2) * 2], dtype="<i2").astype(np.float32) / 32768.0
    if ch > 1:
        a = a.reshape(-1, ch).mean(axis=1)
    wav = torch.from_numpy(a.copy())
    return torchaudio.functional.resample(wav, sr, 16000) if sr != 16000 else wav


def best_per_class(path):
    wav = mono16k(path)
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


results = {}
for path in sorted(glob.glob(f"{CLIPS}/*.wav")):
    name = os.path.basename(path)
    if name not in TRUTH:
        continue
    vec = best_per_class(path)
    scores = {w: float(vec[idx[w]]) for w in watch}
    top = sorted(((float(vec[i]), labels[i]) for i in np.argsort(-vec)[:6]), reverse=True)
    results[name] = {"truth": TRUTH[name], "scores": scores, "top": top}

for name, r in sorted(results.items(), key=lambda kv: kv[1]["truth"]):
    s = r["scores"]
    print(f"\n{r['truth']:<26} {name}")
    print("   top-6:      " + ", ".join(f"{lab} {sc:.2f}" for sc, lab in r["top"]))
    interesting = {k: v for k, v in s.items() if v >= 0.05 and k != "Vehicle"}
    ranked = sorted(interesting.items(), key=lambda kv: -kv[1])[:8]
    print(f"   Vehicle {s['Vehicle']:.2f} | " + ", ".join(f"{k} {v:.2f}" for k, v in ranked))

with open("/root/gotmp/subclass_scores.json", "w") as fh:
    json.dump(results, fh, indent=1)
print("\nwrote /root/gotmp/subclass_scores.json")
