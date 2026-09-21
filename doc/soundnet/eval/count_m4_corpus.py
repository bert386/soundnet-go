#!/usr/bin/env python3
"""How much aircraft training data does the station already hold?

M4 was written as depending on M6's corpus, which is collecting at a few clips a
day. But every identified detection already carries the aircraft's ICAO type
code in its enrichment, and its clip is still on disk - so the corpus may exist
already, scattered across the detections table.

Counts what is recoverable, grouped the way a jet/prop/helicopter head would
need it.
"""
import collections
import json
import os
import subprocess

BASE = 'http://localhost:8080/api/v2'
CLIPS = '/home/pi/soundnet/clips'

# ICAO type designators to engine class, for the traffic this station sees.
JET = {'B738', 'A320', 'A21N', 'A20N', 'B789', 'A333', 'B744', 'E190', 'B38M'}
PROP = {'P28A', 'C208', 'DA40', 'RV7', 'C172', 'C182', 'BE20', 'SR22', 'PC12', 'AT8T'}
HELI = {'A139', 'EC30', 'R44', 'B407', 'AS50', 'H125'}


def get(path):
    raw = subprocess.run(['curl', '-s', f'{BASE}{path}'], capture_output=True, text=True).stdout
    return json.loads(raw) if raw.strip() else None


def category(type_code):
    if type_code in JET:
        return 'jet'
    if type_code in PROP:
        return 'prop'
    if type_code in HELI:
        return 'helicopter'
    return f'unmapped:{type_code}' if type_code else 'untyped'


rows = []
for date in ('2026-09-21', '2026-09-20', '2026-09-19'):
    data = get(f'/detections?queryType=all&numResults=500&date={date}')
    if data:
        rows.extend(data['data'])

events = [r for r in rows if r.get('eventDisplayName')]
print(f'{len(rows)} detections, {len(events)} events\n')

counts = collections.Counter()
with_clip = collections.Counter()
types = collections.Counter()

for r in events:
    detail = get(f"/soundnet/detections/{r['id']}")
    enrich = (detail or {}).get('enrichment') or []
    if not enrich:
        continue
    attrs = enrich[0].get('attributes') or {}
    tc = attrs.get('type_code') or ''
    cat = category(tc)
    counts[cat] += 1
    types[tc or '(none)'] += 1

    clip = r.get('clipName')
    if clip:
        # Clips live under clips/YYYY/MM/.
        y, m = r['date'][:4], r['date'][5:7]
        if os.path.exists(os.path.join(CLIPS, y, m, os.path.basename(clip))):
            with_clip[cat] += 1

print('identified detections by engine class (and how many still have a clip):')
for cat, n in counts.most_common():
    print(f'  {cat:<22} {n:>4}   clips on disk: {with_clip[cat]}')

print('\ntype codes seen:')
for tc, n in types.most_common(15):
    print(f'  {tc:<8} {n}')
