"""Compare the fused CED export with the sherpa oracle on 3-second windows.

The fused graph is fixed-length by construction. CED interpolates its positional
embeddings from the input length, and tracing bakes that grid in, so the export
is valid for the window it was traced with and no other - feeding a 15 s clip to
a 3 s graph fails with a broadcast error, which is at least loud rather than
silent.

That is the right contract here rather than a limitation: SoundNet's analysis
window is three seconds, shared with BirdNET so that a detection and its
diagnostics describe the same audio. YAMNet is used the same way, at a fixed
15600 samples.

So both sides are given identical 3 s, 16 kHz mono windows cut from the middle
of each clip.
"""
import glob
import os
import re
import subprocess
import sys
import wave

import numpy as np
import onnxruntime as ort
import torch
import torchaudio

MODELS = "/root/gotmp/models"
SHERPA = f"{MODELS}/sherpa-onnx-v1.13.8-linux-x64-shared-no-tts"
CED = f"{MODELS}/sherpa-onnx-ced-tiny-audio-tagging-2024-04-19"
SINGLE = f"{MODELS}/ced_tiny_fused_single.onnx"
WORK = "/root/gotmp/win3s"
os.makedirs(WORK, exist_ok=True)

WINDOW = 48000  # 3 s at 16 kHz

labels = []
with open(f"{CED}/class_labels_indices.csv") as f:
    next(f)
    for line in f:
        labels.append(line.rstrip("\n").split(",", 2)[2].strip().strip('"'))

env = dict(os.environ, LD_LIBRARY_PATH=f"{SHERPA}/lib")
pat = re.compile(r'AudioEvent\(name="([^"]+)", index=(\d+), prob=([0-9.e-]+)\)')
sess = ort.InferenceSession(SINGLE, providers=["CPUExecutionProvider"])
inp = sess.get_inputs()[0].name


def read_wav_mono(path):
    with wave.open(path, "rb") as w:
        n, sw, ch, sr = w.getnframes(), w.getsampwidth(), w.getnchannels(), w.getframerate()
        raw = w.readframes(n)
    a = np.frombuffer(raw[: (len(raw) // 2) * 2], dtype="<i2").astype(np.float32) / 32768.0
    if ch > 1:
        a = a.reshape(-1, ch).mean(axis=1)
    return torch.from_numpy(a.copy()), sr


def middle_window(path):
    wav, sr = read_wav_mono(path)
    if sr != 16000:
        wav = torchaudio.functional.resample(wav, sr, 16000)
    if wav.numel() < WINDOW:
        wav = torch.nn.functional.pad(wav, (0, WINDOW - wav.numel()))
    start = max(0, (wav.numel() - WINDOW) // 2)
    return wav[start:start + WINDOW]


def write_wav(win, path):
    pcm = (win.clamp(-1, 1) * 32767).to(torch.int16).numpy()
    with wave.open(path, "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(16000)
        w.writeframes(pcm.tobytes())


def oracle(path, k=5):
    r = subprocess.run(
        [f"{SHERPA}/bin/sherpa-onnx-offline-audio-tagging",
         f"--ced-model={CED}/model.onnx",
         f"--labels={CED}/class_labels_indices.csv",
         f"--top-k={k}", path],
        capture_output=True, text=True, env=env)
    return [(m[0], float(m[2])) for m in pat.findall(r.stdout + r.stderr)]


def fused(win, k=5):
    out = sess.run(None, {inp: win.unsqueeze(0).numpy().astype(np.float32)})[0][0]
    top = np.argsort(-out)[:k]
    return [(labels[i], float(out[i])) for i in top]


clips = sys.argv[1:] or sorted(glob.glob("/root/gotmp/clips/*.wav"))[:10]
agree = total = 0
deltas = []
for path in clips:
    win = middle_window(path)
    tmp = os.path.join(WORK, "w_" + os.path.basename(path))
    write_wav(win, tmp)
    o, f = oracle(tmp), fused(win)
    if not o or not f:
        print(f"{os.path.basename(path)}: scoring failed")
        continue
    total += 1
    same = o[0][0] == f[0][0]
    agree += same
    # Compare the oracle's top class score against the fused score for the same
    # class, which is the like-for-like number.
    fmap = {n: p for n, p in fused(win, 527)}
    deltas.append(abs(fmap.get(o[0][0], 0.0) - o[0][1]))
    print(os.path.basename(path))
    print("   sherpa: " + ", ".join(f"{n} {p:.3f}" for n, p in o[:3]))
    print("   fused : " + ", ".join(f"{n} {p:.3f}" for n, p in f[:3]))
    print(f"   top-1 agrees: {same}")

print()
print(f"top-1 agreement: {agree}/{total}")
if deltas:
    print(f"median |score delta| on the oracle's top class: {sorted(deltas)[len(deltas)//2]:.3f}")
    print(f"max    |score delta|: {max(deltas):.3f}")
