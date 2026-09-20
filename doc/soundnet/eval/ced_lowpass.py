"""Does low-pass + normalise rescue the faint aircraft CED misses?

Tests items 2 and 4 of the plan together, offline, before either touches the
station. Order matters and is the point: low-pass first so the loudest birdsong
transient is removed, normalise second so the gain is set by what is left.
Normalising first would be hostage to the bird.
"""
import json
import math
import os
import re
import struct
import subprocess
import wave

MODELS = "/root/gotmp/models"
SHERPA = f"{MODELS}/sherpa-onnx-v1.13.8-linux-x64-shared-no-tts"
CED = f"{MODELS}/sherpa-onnx-ced-tiny-audio-tagging-2024-04-19"
CLIPS = "/root/gotmp/clips"
WORK = "/root/gotmp/lp"
os.makedirs(WORK, exist_ok=True)

TRUTH = {
    726: "aircraft", 730: "aircraft", 733: "aircraft", 759: "aircraft",
    805: "aircraft", 824: "aircraft", 828: "aircraft",
    905: "vehicle",
    720: "other", 725: "other", 817: "other", 826: "other", 838: "other",
    880: "other", 929: "other", 932: "other", 937: "other",
}

AIRCRAFT = {
    "Aircraft", "Aircraft engine", "Jet engine",
    "Propeller, airscrew", "Helicopter", "Fixed-wing aircraft, airplane",
}

rows = json.load(open("/root/gotmp/dets.json"))["data"]
clip_for = {r["id"]: r.get("clipName") for r in rows}

env = dict(os.environ, LD_LIBRARY_PATH=f"{SHERPA}/lib")
pat = re.compile(r'AudioEvent\(name="([^"]+)", index=(\d+), prob=([0-9.e-]+)\)')


def biquad_lowpass(x, sr, fc, q=0.7071):
    """One second-order Butterworth section, applied in place."""
    w0 = 2 * math.pi * fc / sr
    alpha = math.sin(w0) / (2 * q)
    cw = math.cos(w0)
    b0, b1, b2 = (1 - cw) / 2, 1 - cw, (1 - cw) / 2
    a0, a1, a2 = 1 + alpha, -2 * cw, 1 - alpha
    b0, b1, b2, a1, a2 = b0 / a0, b1 / a0, b2 / a0, a1 / a0, a2 / a0
    x1 = x2 = y1 = y2 = 0.0
    out = [0.0] * len(x)
    for i, s in enumerate(x):
        y = b0 * s + b1 * x1 + b2 * x2 - a1 * y1 - a2 * y2
        out[i] = y
        x2, x1 = x1, s
        y2, y1 = y1, y
    return out


def process(src, dst, fc=1200.0, peak_target_db=-3.0):
    with wave.open(src, "rb") as w:
        n, sw, ch, sr = w.getnframes(), w.getsampwidth(), w.getnchannels(), w.getframerate()
        raw = w.readframes(n)
    count = len(raw) // 2
    s = list(struct.unpack("<%dh" % count, raw[: count * 2]))
    if ch > 1:
        s = s[::ch]
    x = [v / 32768.0 for v in s]

    # Low-pass FIRST: the aircraft lives below ~1.2 kHz and the birds above it.
    x = biquad_lowpass(x, sr, fc)
    x = biquad_lowpass(x, sr, fc)

    # Normalise SECOND, on what survived.
    pk = max(abs(v) for v in x) or 1e-9
    gain = (10 ** (peak_target_db / 20.0)) / pk
    x = [max(-1.0, min(1.0, v * gain)) for v in x]

    out = struct.pack("<%dh" % len(x), *[int(v * 32767) for v in x])
    with wave.open(dst, "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(sr)
        w.writeframes(out)
    return 20 * math.log10(gain) if gain > 0 else 0.0


def score(path):
    r = subprocess.run(
        [f"{SHERPA}/bin/sherpa-onnx-offline-audio-tagging",
         f"--ced-model={CED}/model.onnx",
         f"--labels={CED}/class_labels_indices.csv",
         "--top-k=12", path],
        capture_output=True, text=True, env=env)
    return [(m[0], float(m[2])) for m in pat.findall(r.stdout + r.stderr)]


def aircraft_score(ev):
    return max((p for n, p in ev if n in AIRCRAFT), default=0.0)


print(f"{'id':>5} {'truth':<9} {'raw aircraft':>13} {'LP+norm aircraft':>17} {'gain dB':>8}  top-1 after")
print("-" * 96)
before_hits = after_hits = 0
before_fp = after_fp = 0
for det_id, kind in sorted(TRUTH.items()):
    name = clip_for.get(det_id)
    if not name:
        continue
    src = os.path.join(CLIPS, name)
    if not os.path.exists(src):
        continue
    dst = os.path.join(WORK, name)
    g = process(src, dst)
    raw_ev, lp_ev = score(src), score(dst)
    a0, a1 = aircraft_score(raw_ev), aircraft_score(lp_ev)
    top = lp_ev[0] if lp_ev else ("?", 0)
    if kind == "aircraft":
        before_hits += a0 >= 0.30
        after_hits += a1 >= 0.30
    else:
        before_fp += a0 >= 0.30
        after_fp += a1 >= 0.30
    arrow = "  ^" if a1 > a0 + 0.05 else ("  v" if a1 < a0 - 0.05 else "   ")
    print(f"{det_id:>5} {kind:<9} {a0:>13.3f} {a1:>17.3f}{arrow} {g:>7.1f}  "
          f"{top[0][:24]} {top[1]:.2f}")

print()
print(f"aircraft detected (>=0.30):  raw {before_hits}/7   low-pass+norm {after_hits}/7")
print(f"false positives on the rest: raw {before_fp}/10  low-pass+norm {after_fp}/10")
