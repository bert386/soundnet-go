"""Peak levels across recent detections of every kind.

The gain ceiling is set by the loudest thing the station records, which is a
close bird, not the quiet aircraft the annotated set is full of. Sampling only
the non-bird clips would pick a gain that clips every myna.
"""
import json
import math
import os
import struct
import subprocess
import wave

BASE = "http://192.168.3.89:8080"
OUT = "/root/gotmp/clips"
os.makedirs(OUT, exist_ok=True)

rows = json.load(open("/root/gotmp/dets.json"))["data"]
want = [r for r in rows if r.get("clipName")][:70]


def fetch(det_id, name):
    path = os.path.join(OUT, name)
    if os.path.exists(path) and os.path.getsize(path) > 1000:
        return path
    subprocess.run(
        ["curl", "-s", "-m", "60", f"{BASE}/api/v2/audio/{det_id}", "-o", path],
        capture_output=True,
    )
    return path if os.path.exists(path) and os.path.getsize(path) > 1000 else None


def levels(path):
    try:
        with wave.open(path, "rb") as w:
            n, sw, ch = w.getnframes(), w.getsampwidth(), w.getnchannels()
            raw = w.readframes(n)
    except Exception:
        return None
    if sw != 2:
        return None
    count = len(raw) // 2
    s = struct.unpack("<%dh" % count, raw[: count * 2])
    if ch > 1:
        s = s[::ch]
    peak = max(abs(v) for v in s) or 1
    tot = 0
    for v in s:
        tot += v * v
    rms = (tot / len(s)) ** 0.5 or 1
    return 20 * math.log10(peak / 32767.0), 20 * math.log10(rms / 32767.0)


results = []
for r in want:
    p = fetch(r["id"], r["clipName"])
    if not p:
        continue
    got = levels(p)
    if got:
        results.append((got[0], got[1], r["id"], r["scientificName"], r["confidence"]))

results.sort(reverse=True)
print(f"measured {len(results)} clips\n")
print("loudest 12:")
for pk, rm, i, sci, c in results[:12]:
    print(f"  id={i:<5} {sci:<28} conf={c:.2f}  peak {pk:6.1f}  rms {rm:6.1f}")

peaks = [r[0] for r in results]
rmss = sorted(r[1] for r in results)
print()
print(f"max peak      : {max(peaks):6.1f} dBFS")
print(f"99th pct peak : {sorted(peaks)[int(len(peaks) * 0.99) - 1]:6.1f} dBFS")
print(f"95th pct peak : {sorted(peaks)[int(len(peaks) * 0.95) - 1]:6.1f} dBFS")
print(f"median rms    : {rmss[len(rmss) // 2]:6.1f} dBFS")
print()
print(f"headroom before the loudest clip clips: {-max(peaks):.1f} dB")
