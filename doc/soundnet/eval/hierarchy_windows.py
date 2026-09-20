"""The same question as hierarchy_sweep.py, asked the way the station asks it.

The first sweep took each class's best score over a whole clip. That is the
right shape for "does this clip contain an aircraft", and the wrong shape for
the decision actually being made, which happens inside one three-second window
with only that window's scores. A ratio that holds between two per-clip maxima
taken from different windows need not hold in any single window.

So this scores window by window and counts windows, not clips. A rule is only
worth shipping if it fires on windows of aircraft clips and stays quiet on
windows of everything else.
"""
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
CLIPS = "/root/gotmp/clips"

WINDOW = 48000
HOP = 24000

TRUTH = {
    674: "aircraft", 679: "aircraft", 686: "aircraft", 711: "aircraft",
    715: "aircraft", 726: "aircraft", 730: "aircraft", 733: "aircraft",
    759: "aircraft", 805: "aircraft", 824: "aircraft", 828: "aircraft",
    905: "vehicle",
    817: "other", 720: "other", 693: "other", 702: "other", 725: "other",
    838: "other", 880: "other", 929: "other", 932: "other", 937: "other",
    826: "other",
}
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

# The bar a child must clear on its own to be recorded at all. Below it the
# parent must be kept, or a sound the station detected becomes undetected.
CANDIDATE_FLOOR = 0.15

labels = []
with open(f"{CED}/class_labels_indices.csv") as fh:
    next(fh)
    for line in fh:
        labels.append(line.rstrip("\n").split(",", 2)[2].strip().strip('"'))
index_of = {name: i for i, name in enumerate(labels)}

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


def windows(path):
    wav = read_mono_16k(path)
    if wav.numel() < WINDOW:
        wav = torch.nn.functional.pad(wav, (0, WINDOW - wav.numel()))
    for start in range(0, max(1, wav.numel() - WINDOW + 1), HOP):
        win = wav[start:start + WINDOW]
        if win.numel() < WINDOW:
            return
        yield sess.run(None, {inp: win.unsqueeze(0).numpy().astype(np.float32)})[0][0]


rows = json.load(open("/root/gotmp/dets.json"))["data"]
by_id = {r["id"]: os.path.basename(r.get("clipName") or "") for r in rows}

samples = []  # (truth, parent_score, child_score)
for det_id, truth in sorted(TRUTH.items()):
    name = EXTRA_CLIPS.get(det_id) or by_id.get(det_id, "")
    path = os.path.join(CLIPS, name) if name else ""
    if not path or not os.path.exists(path):
        continue
    for vec in windows(path):
        parent = float(vec[index_of[PARENT]])
        child = max(float(vec[index_of[n]]) for n in AIRCRAFT)
        samples.append((truth, parent, child))

pos = [s for s in samples if s[0] == "aircraft"]
neg = [s for s in samples if s[0] != "aircraft"]
print(f"{len(samples)} windows: {len(pos)} from aircraft clips, {len(neg)} from the rest\n")

# Only windows where the parent would actually have been recorded matter: the
# rule exists to stop a Vehicle row being written, and there is nothing to stop
# when none would be.
print("Rule: drop Vehicle when a child class clears both its own floor and a")
print(f"fraction of Vehicle. Child floor {CANDIDATE_FLOOR} (a child below it is")
print("not recorded either, so dropping the parent would lose the detection).\n")

print(f"{'vehBar':>7} {'ratio':>6} {'fires+':>7} {'of':>5} {'fires-':>7} {'of':>5}")
for veh_bar in [0.15, 0.20, 0.25, 0.30]:
    for ratio in [0.25, 0.33, 0.40, 0.50, 0.60, 0.75]:
        def fires(s):
            _, parent, child = s
            return parent >= veh_bar and child >= CANDIDATE_FLOOR and child >= ratio * parent
        tp = sum(1 for s in pos if fires(s))
        fp = sum(1 for s in neg if fires(s))
        print(f"{veh_bar:>7.2f} {ratio:>6.2f} {tp:>7} {len(pos):>5} {fp:>7} {len(neg):>5}")

# The margin that matters: among windows where the parent clears the floor and
# the child clears its own, how do the ratios separate?
print("\nRatios in windows where both floors are cleared:")
for label, group in (("aircraft", pos), ("other", neg)):
    ratios = sorted(
        (c / p for (_, p, c) in group if p >= 0.20 and c >= CANDIDATE_FLOOR),
        reverse=True,
    )
    shown = ", ".join(f"{r:.2f}" for r in ratios[:12])
    print(f"  {label:<9} n={len(ratios):<3} {shown}")
