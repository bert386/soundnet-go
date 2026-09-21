#!/usr/bin/env python3
"""Build M4's training corpus from detections ADS-B has already identified.

M4 is the head that will tell a jet from a propeller from a helicopter by ear.
It was scoped to wait on the M6 collector, which captures a few clips a day.
But every detection runtime enrichment identified already carries the
aircraft's ICAO type code, and its clip is still on disk: 532 of them on the
deployment station after three days, 95 different aircraft. The corpus exists;
it is just scattered across the detections table.

This gathers it, in the same layout the M6 collector writes, so M4 trains from
one corpus with one reader:

    <out>/<TYPE>/<YYYYmmddTHHMMSS>_<hex>_<callsign>.wav
    <out>/<TYPE>/<YYYYmmddTHHMMSS>_<hex>_<callsign>.json

Label quality matters more than volume, because a wrong label is invisible once
it is in the corpus. So an example is kept only if:

  - exactly one aircraft was in the search box    (the identity is not a guess)
  - its type maps to an engine class               (never guessed; reported)
  - the audio did not clip                         (clipping here is wind on the
                                                    capsule, not an aeroplane:
                                                    2 of 108 aircraft detections
                                                    clipped, 39 of 167 "thunder")
  - it is the closest approach of its pass         (one aeroplane crossing the
                                                    sky makes several windows;
                                                    they are near-duplicates)

Read-only against the database. Clips are copied, never moved.

    python3 m4_backfill.py --dry-run
    python3 m4_backfill.py --out /home/pi/soundnet/corpus/backfill
"""
from __future__ import annotations

import argparse
import csv
import json
import os
import shutil
import sqlite3
import sys
from collections import Counter, defaultdict
from dataclasses import dataclass, field
from datetime import datetime, timezone

DEFAULT_DB = "/home/pi/soundnet/birdnet.db"
DEFAULT_CLIPS = "/home/pi/soundnet/clips"
DEFAULT_OUT = "/home/pi/soundnet/corpus/backfill"
DEFAULT_TYPES = os.path.join(
    os.path.dirname(os.path.abspath(__file__)), "..", "..", "internal", "aircrafttype", "engine_types.csv"
)

# -0.1 dBFS: the same line the station's clipping counts were drawn at, and the
# one the runtime's rejectclipped rule uses.
CLIP_DBFS = -0.1

# A new pass starts when the same aircraft goes unheard for this long. A
# flypast is audible for 40-80 s here; two minutes apart is a different pass.
PASS_GAP_S = 120

AIRCRAFT_CLASSES = {"aircraft", "fixed-wing", "propeller", "helicopter", "jet", "airplane"}


@dataclass
class Row:
    """One identified detection, as read from the database."""

    detection_id: int
    clip_name: str
    detected_at: float  # unix seconds
    confidence: float
    heard_as: str  # the classifier's label, lower case
    adsb: dict
    peak_dbfs: float | None


@dataclass
class Selection:
    kept: list[tuple[Row, dict]] = field(default_factory=list)  # (row, engine type row)
    rejected: Counter = field(default_factory=Counter)
    unmapped: Counter = field(default_factory=Counter)


def load_types(path: str) -> dict[str, dict]:
    with open(path, encoding="utf-8") as fh:
        lines = [ln for ln in fh if not ln.lstrip().startswith("#")]
    out = {}
    for r in csv.DictReader(lines):
        out[r["type"].strip().upper()] = {k: v.strip() for k, v in r.items()}
    return out


def select(rows: list[Row], types: dict[str, dict], per_pass: int = 1) -> Selection:
    """Apply the label-quality rules. Pure: no I/O, so it can be tested."""
    sel = Selection()
    eligible: list[tuple[Row, dict]] = []

    for row in rows:
        code = (row.adsb.get("type_code") or "").strip().upper()
        if not code:
            sel.rejected["no type code"] += 1
            continue
        engine = types.get(code)
        if engine is None:
            sel.rejected["type not in the engine table"] += 1
            sel.unmapped[code] += 1
            continue
        if row.adsb.get("candidates_in_box") != 1:
            sel.rejected["more than one aircraft in range"] += 1
            continue
        if row.peak_dbfs is None:
            sel.rejected["no level measured"] += 1
            continue
        if row.peak_dbfs >= CLIP_DBFS:
            sel.rejected["clipped (wind on the microphone)"] += 1
            continue
        eligible.append((row, engine))

    # Group into passes per aircraft, then keep the closest approaches.
    by_hex: dict[str, list[tuple[Row, dict]]] = defaultdict(list)
    for item in eligible:
        by_hex[item[0].adsb.get("hex", "")].append(item)

    for items in by_hex.values():
        items.sort(key=lambda it: it[0].detected_at)
        passes: list[list[tuple[Row, dict]]] = []
        for item in items:
            if passes and item[0].detected_at - passes[-1][-1][0].detected_at <= PASS_GAP_S:
                passes[-1].append(item)
            else:
                passes.append([item])
        for p in passes:
            p.sort(key=lambda it: it[0].adsb.get("slant_range_km", float("inf")))
            sel.kept.extend(p[:per_pass])
            if len(p) > per_pass:
                sel.rejected["another window of the same pass"] += len(p) - per_pass

    sel.kept.sort(key=lambda it: it[0].detected_at)
    return sel


def load_rows(db: str) -> list[Row]:
    con = sqlite3.connect(f"file:{db}?mode=ro", uri=True)
    rows = []
    for det_id, clip, at, conf, sci, epay, gpay in con.execute(
        """
        select d.id, d.clip_name, d.detected_at, d.confidence, l.scientific_name,
               e.payload, g.payload
        from soundnet_enrichment e
        join detections d on d.id = e.detection_id
        join labels l on l.id = d.label_id
        left join soundnet_diagnostics g on g.detection_id = d.id
        where e.provider = 'adsb'
        """
    ):
        if not clip:
            continue
        adsb = json.loads(epay)
        peak = None
        if gpay:
            level = (json.loads(gpay).get("level") or {})
            peak = level.get("peak_dbfs")
        seconds = float(at) / 1000.0 if at and at > 1e12 else float(at or 0)
        rows.append(Row(det_id, clip, seconds, conf, (sci or "").lower(), adsb, peak))
    return rows


def sidecar(row: Row, engine: dict) -> dict:
    a = row.adsb
    meta = {
        "captured_at": datetime.fromtimestamp(row.detected_at, timezone.utc).isoformat(),
        "icao24": a.get("hex"),
        "callsign": (a.get("callsign") or "").strip(),
        "registration": a.get("registration"),
        "type_code": a.get("type_code"),
        "type_name": a.get("type_name"),
        "operator": a.get("operator"),
        "engine": engine["engine"],
        "engine_class": engine["coarse"],
        "altitude_m": a.get("altitude_m"),
        "slant_range_km": a.get("slant_range_km"),
        "acoustic_lag_s": a.get("lag_correction_s"),
        "candidates_in_box": a.get("candidates_in_box"),
        "peak_dbfs": row.peak_dbfs,
        "heard_as": row.heard_as,
        "heard_as_aircraft": row.heard_as in AIRCRAFT_CLASSES,
        "classifier_confidence": row.confidence,
        "detection_id": row.detection_id,
        "source_clip": row.clip_name,
        "label_provenance": "adsb-backfill",
        "selection": "single aircraft in range; not clipped; closest window of its pass",
    }
    return {k: v for k, v in meta.items() if v not in (None, "")}


def write(sel: Selection, out: str, clips: str) -> int:
    written = 0
    for row, engine in sel.kept:
        src = row.clip_name if os.path.isabs(row.clip_name) else os.path.join(clips, row.clip_name)
        if not os.path.exists(src):
            continue
        code = "".join(c for c in engine["type"].upper() if c.isalnum()) or "_untyped"
        stamp = datetime.fromtimestamp(row.detected_at, timezone.utc).strftime("%Y%m%dT%H%M%S")
        callsign = "".join(c for c in (row.adsb.get("callsign") or "") if c.isalnum())
        base = "_".join(p for p in (stamp, row.adsb.get("hex", "unknown"), callsign) if p)
        folder = os.path.join(out, code)
        os.makedirs(folder, exist_ok=True)
        shutil.copy2(src, os.path.join(folder, base + ".wav"))
        with open(os.path.join(folder, base + ".json"), "w", encoding="utf-8") as fh:
            json.dump(sidecar(row, engine), fh, indent=2)
        written += 1
    return written


def report(rows: list[Row], sel: Selection) -> None:
    print(f"identified detections read: {len(rows)}")
    print("rejected:")
    for reason, n in sel.rejected.most_common():
        print(f"  {n:5d}  {reason}")
    if sel.unmapped:
        print("types missing from the engine table (add them, do not guess):")
        for code, n in sel.unmapped.most_common():
            print(f"  {code:8s} {n}")

    by_class: dict[str, Counter] = defaultdict(Counter)
    aircraft: dict[str, set] = defaultdict(set)
    for row, engine in sel.kept:
        by_class[engine["coarse"]][engine["type"]] += 1
        aircraft[engine["coarse"]].add(row.adsb.get("hex"))
    print(f"\nkept: {len(sel.kept)} examples")
    for cls in ("jet", "prop", "helicopter"):
        n = sum(by_class[cls].values())
        types = ", ".join(f"{t} {c}" for t, c in by_class[cls].most_common())
        print(f"  {cls:10s} {n:4d} examples, {len(aircraft[cls]):3d} aircraft   [{types}]")


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--db", default=DEFAULT_DB)
    ap.add_argument("--clips", default=DEFAULT_CLIPS)
    ap.add_argument("--out", default=DEFAULT_OUT)
    ap.add_argument("--types", default=DEFAULT_TYPES, help="engine_types.csv")
    ap.add_argument("--per-pass", type=int, default=1, help="closest windows kept per pass")
    ap.add_argument("--dry-run", action="store_true", help="report only, write nothing")
    args = ap.parse_args(argv)

    types = load_types(args.types)
    rows = load_rows(args.db)
    sel = select(rows, types, per_pass=args.per_pass)
    report(rows, sel)
    if args.dry_run:
        print("\ndry run: nothing written")
        return 0
    n = write(sel, args.out, args.clips)
    print(f"\nwrote {n} examples to {args.out}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
