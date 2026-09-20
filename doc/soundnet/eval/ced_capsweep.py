"""Sweep the normalisation gain cap.

Uncapped peak-normalisation after low-passing lifts the aircraft clips AND the
quiet junk, because a clip that needs 30 dB to reach the target is mostly noise
and 30 dB of noise looks like low-frequency rumble. The clips that actually
contain an aircraft needed only 9-13 dB. A cap should therefore keep the signal
and leave the noise where it was - which is a testable claim, not a hope.

Filters once per clip and reuses the result across caps: the biquad pass is the
expensive part and it does not depend on the cap.
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
AIRCRAFT = {"Aircraft", "Aircraft engine", "Jet engine",
            "Propeller, airscrew", "Helicopter", "Fixed-wing aircraft, airplane"}
CAPS = [0, 6, 12, 18, 99]

rows = json.load(open("/root/gotmp/dets.json"))["data"]
clip_for = {r["id"]: r.get("clipName") for r in rows}
env = dict(os.environ, LD_LIBRARY_PATH=f"{SHERPA}/lib")
pat = re.compile(r'AudioEvent\(name="([^"]+)", index=(\d+), prob=([0-9.e-]+)\)')


def biquad_lowpass(x, sr, fc, q=0.7071):
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


def load_filtered(src, fc=1200.0):
    with wave.open(src, "rb") as w:
        n, sw, ch, sr = w.getnframes(), w.getsampwidth(), w.getnchannels(), w.getframerate()
        raw = w.readframes(n)
    c = len(raw) // 2
    s = list(struct.unpack("<%dh" % c, raw[: c * 2]))
    if ch > 1:
        s = s[::ch]
    x = [v / 32768.0 for v in s]
    x = biquad_lowpass(x, sr, fc)
    x = biquad_lowpass(x, sr, fc)
    return x, sr


def write_capped(x, sr, dst, cap_db, peak_target_db=-3.0):
    pk = max(abs(v) for v in x) or 1e-9
    want = (10 ** (peak_target_db / 20.0)) / pk
    want_db = 20 * math.log10(want)
    used_db = min(want_db, cap_db)
    g = 10 ** (used_db / 20.0)
    y = [max(-1.0, min(1.0, v * g)) for v in x]
    with wave.open(dst, "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(sr)
        w.writeframes(struct.pack("<%dh" % len(y), *[int(v * 32767) for v in y]))
    return used_db


def score(path):
    r = subprocess.run(
        [f"{SHERPA}/bin/sherpa-onnx-offline-audio-tagging",
         f"--ced-model={CED}/model.onnx",
         f"--labels={CED}/class_labels_indices.csv",
         "--top-k=12", path],
        capture_output=True, text=True, env=env)
    ev = [(m[0], float(m[2])) for m in pat.findall(r.stdout + r.stderr)]
    return max((p for n, p in ev if n in AIRCRAFT), default=0.0)


results = {}
for det_id, kind in sorted(TRUTH.items()):
    name = clip_for.get(det_id)
    if not name or not os.path.exists(os.path.join(CLIPS, name)):
        continue
    x, sr = load_filtered(os.path.join(CLIPS, name))
    row = {}
    for cap in CAPS:
        dst = os.path.join(WORK, f"c{cap}_{name}")
        write_capped(x, sr, dst, cap)
        row[cap] = score(dst)
    results[det_id] = (kind, row)
    print(f"  scored {det_id} ({kind})", flush=True)

print()
hdr = "  ".join(f"{('none' if c == 0 else ('inf' if c == 99 else str(c) + 'dB')):>6}" for c in CAPS)
print(f"{'id':>5} {'truth':<9} {hdr}")
print("-" * (16 + 8 * len(CAPS)))
for det_id, (kind, row) in sorted(results.items()):
    cells = "  ".join(f"{row[c]:>6.3f}" for c in CAPS)
    print(f"{det_id:>5} {kind:<9} {cells}")

print()
print(f"{'cap':>6} {'aircraft >=0.30':>16} {'worst non-aircraft':>19} {'margin':>8}")
for c in CAPS:
    air = [row[c] for k, row in results.values() if k == "aircraft"]
    non = [row[c] for k, row in results.values() if k != "aircraft"]
    hits = sum(1 for v in air if v >= 0.30)
    worst = max(non) if non else 0.0
    label = "none" if c == 0 else ("inf" if c == 99 else f"{c}dB")
    print(f"{label:>6} {f'{hits}/{len(air)}':>16} {worst:>19.3f} {min(air) if air else 0:>8.3f}")
