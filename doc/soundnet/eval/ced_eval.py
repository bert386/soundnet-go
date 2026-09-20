"""Score CED-tiny over the operator-labelled clips and compare with YAMNet.

The comparison that matters is not AudioSet mAP, which was measured on someone
else's corpus at someone else's SNR. It is: on these clips, at this station,
does the model put an aircraft in the aircraft classes?
"""
import json
import os
import re
import subprocess

MODELS = "/root/gotmp/models"
SHERPA = f"{MODELS}/sherpa-onnx-v1.13.8-linux-x64-shared-no-tts"
CED = f"{MODELS}/sherpa-onnx-ced-tiny-audio-tagging-2024-04-19"
CLIPS = "/root/gotmp/clips"

# What the operator said, keyed by detection id. "aircraft" means they heard one.
TRUTH = {
    674: ("aircraft", "two propeller aircraft"),
    679: ("aircraft", "propeller aircraft"),
    686: ("aircraft", "loud clear propeller overhead"),
    711: ("aircraft", "distant jet"),
    715: ("aircraft", "distant commercial jet"),
    726: ("aircraft", "high aircraft or distant traffic"),
    730: ("aircraft", "distant aircraft, probably propeller"),
    733: ("aircraft", "distant propeller + helicopter"),
    759: ("aircraft", "propeller aircraft overhead"),
    805: ("aircraft", "turbo-prop passing"),
    824: ("aircraft", "commercial jet"),
    828: ("aircraft", "close propeller aircraft"),
    905: ("vehicle", "large truck"),
    817: ("other", "human whistling"),
    720: ("other", "hammering, furniture"),
    693: ("other", "hammering, tapping"),
    702: ("other", "hammering, faint speech"),
    725: ("other", "rustling grass"),
    838: ("other", "rustling dry grass"),
    880: ("other", "a crow"),
    929: ("other", "rustling dry grass"),
    932: ("other", "rustling dry grass"),
    937: ("other", "rustling dry grass"),
    826: ("other", "mower or traffic (BirdNET called it a bird)"),
}

AIRCRAFT = {
    "Aircraft",
    "Aircraft engine",
    "Jet engine",
    "Propeller, airscrew",
    "Helicopter",
    "Fixed-wing aircraft, airplane",
}

rows = json.load(open("/root/gotmp/dets.json"))["data"]
clip_for = {r["id"]: r.get("clipName") for r in rows}
yamnet_for = {r["id"]: (r["scientificName"], r["confidence"]) for r in rows}

env = dict(os.environ, LD_LIBRARY_PATH=f"{SHERPA}/lib")
pat = re.compile(r'AudioEvent\(name="([^"]+)", index=(\d+), prob=([0-9.e-]+)\)')


def score(path):
    out = subprocess.run(
        [
            f"{SHERPA}/bin/sherpa-onnx-offline-audio-tagging",
            f"--ced-model={CED}/model.onnx",
            f"--labels={CED}/class_labels_indices.csv",
            "--top-k=12",
            path,
        ],
        capture_output=True,
        text=True,
        env=env,
    )
    return [(m[0], float(m[2])) for m in pat.findall(out.stdout + out.stderr)]


print(f"{'id':>5} {'truth':<9} {'YAMNet said':<26} {'CED top-1':<30} {'CED aircraft':>12}")
print("-" * 92)
hits = miss = 0
fp = tn = 0
for det_id, (kind, note) in sorted(TRUTH.items()):
    name = clip_for.get(det_id)
    if not name:
        continue
    path = os.path.join(CLIPS, name)
    if not os.path.exists(path):
        continue
    ev = score(path)
    if not ev:
        print(f"{det_id:>5} (scoring failed)")
        continue
    top = ev[0]
    air = max((p for n, p in ev if n in AIRCRAFT), default=0.0)
    ysci, yconf = yamnet_for.get(det_id, ("?", 0))
    marker = ""
    if kind == "aircraft":
        if air >= 0.30:
            hits += 1
            marker = "HIT"
        else:
            miss += 1
            marker = "miss"
    else:
        if air >= 0.30:
            fp += 1
            marker = "FP"
        else:
            tn += 1
    print(
        f"{det_id:>5} {kind:<9} {ysci + ' ' + format(yconf, '.2f'):<26} "
        f"{top[0][:26] + ' ' + format(top[1], '.2f'):<30} {air:>8.3f} {marker}"
    )

print()
print(f"aircraft clips: {hits} detected at >=0.30 in an aircraft class, {miss} missed")
print(f"non-aircraft  : {fp} false positives, {tn} correctly quiet")
