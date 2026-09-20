"""Ratio of the aircraft classes to Thunder/Thunderstorm on clips ADS-B calls jets.

The Vehicle case was measured on the operator's labelled clips. Thunder was not:
those detections were reviewed but their clips were not retained. These were
recorded tonight and resolved to aircraft by ADS-B, which is an authoritative
label of a different kind - weaker than a person listening, much stronger than a
model agreeing with itself.
"""
import glob
import os
import wave

import numpy as np
import onnxruntime as ort
import torch
import torchaudio

MODELS = "/root/gotmp/models"
CED = f"{MODELS}/sherpa-onnx-ced-tiny-audio-tagging-2024-04-19"
SINGLE = f"{MODELS}/ced_tiny_fused_single.onnx"
WINDOW, HOP = 48000, 24000
AIRCRAFT = {
    "Aircraft", "Aircraft engine", "Jet engine",
    "Propeller, airscrew", "Helicopter", "Fixed-wing aircraft, airplane",
}

labels = []
with open(f"{CED}/class_labels_indices.csv") as fh:
    next(fh)
    for line in fh:
        labels.append(line.rstrip("\n").split(",", 2)[2].strip().strip('"'))
idx = {name: i for i, name in enumerate(labels)}

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


ratios = []
for path in sorted(glob.glob("/root/gotmp/thunder/*.wav")):
    wav = mono16k(path)
    if wav.numel() < WINDOW:
        wav = torch.nn.functional.pad(wav, (0, WINDOW - wav.numel()))
    print(os.path.basename(path))
    for start in range(0, max(1, wav.numel() - WINDOW + 1), HOP):
        win = wav[start:start + WINDOW]
        if win.numel() < WINDOW:
            break
        out = sess.run(None, {inp: win.unsqueeze(0).numpy().astype(np.float32)})[0][0]
        storm = max(float(out[idx["Thunderstorm"]]), float(out[idx["Thunder"]]))
        veh = float(out[idx["Vehicle"]])
        air = max(float(out[idx[name]]) for name in AIRCRAFT)
        r_storm = air / storm if storm > 0 else 0.0
        r_veh = air / veh if veh > 0 else 0.0
        if storm >= 0.15:
            ratios.append(r_storm)
        print(f"   storm {storm:.3f}  vehicle {veh:.3f}  aircraft {air:.3f}"
              f"   air/storm {r_storm:.2f}  air/veh {r_veh:.2f}")

if ratios:
    ratios.sort()
    print(f"\nair/storm over {len(ratios)} windows where Thunder cleared 0.15:")
    print(f"  min {ratios[0]:.2f}  median {ratios[len(ratios) // 2]:.2f}  max {ratios[-1]:.2f}")
