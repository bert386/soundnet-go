#!/usr/bin/env python3
"""How many detections does one flyby actually produce, and what ties them together?

The operator sees a single pass arriving as several rows under different class
names. Before designing a grouping rule, this asks the data what a pass looks
like: how long it spans, how many rows it makes, which classes, and - the part
that matters - whether the rows that belong together can be recognised by
something authoritative rather than by a guess about timing.
"""
import json
import subprocess
import sys
from datetime import datetime

DATE = sys.argv[1] if len(sys.argv) > 1 else '2026-09-21'
BASE = 'http://localhost:8080/api/v2'


def get(path):
    out = subprocess.run(['curl', '-s', f'{BASE}{path}'], capture_output=True, text=True).stdout
    return json.loads(out) if out.strip() else None


rows = get(f'/detections?queryType=all&numResults=500&date={DATE}')['data']
events = [r for r in rows if r.get('eventDisplayName')]
events.sort(key=lambda r: r['time'])
print(f'{len(events)} event rows on {DATE}\n')

# Pull the identity for each, which is the only authoritative grouping key.
hexes = {}
for r in events:
    detail = get(f"/soundnet/detections/{r['id']}")
    if not detail:
        continue
    for e in detail.get('enrichment') or []:
        attrs = e.get('attributes') or {}
        if attrs.get('hex'):
            hexes[r['id']] = attrs['hex']
            break


def seconds(t):
    return (datetime.strptime(t, '%H:%M:%S') - datetime(1900, 1, 1)).total_seconds()


# Cluster on time alone, the naive rule, and see what it would have grouped.
GAP = 30
clusters, current = [], []
for r in events:
    if current and seconds(r['time']) - seconds(current[-1]['time']) > GAP:
        clusters.append(current)
        current = []
    current.append(r)
if current:
    clusters.append(current)

print(f'{len(clusters)} clusters at a {GAP}s gap\n')
multi = [c for c in clusters if len(c) > 1]
print(f'{len(multi)} clusters hold more than one row; '
      f'{sum(len(c) for c in multi)} of {len(events)} rows are in them\n')

for c in multi[:12]:
    span = seconds(c[-1]['time']) - seconds(c[0]['time'])
    ids = {hexes.get(r['id']) for r in c if hexes.get(r['id'])}
    print(f"{c[0]['time']}-{c[-1]['time']}  {len(c)} rows  span {span:.0f}s  "
          f"hex={ids or '-'}")
    for r in c:
        print(f"    {r['id']:>5} {r['time']} {r['commonName']:<22} {r['confidence']:.2f} "
              f"-> {r.get('resolvedDomain') or '-'}  {hexes.get(r['id']) or ''}")

# The question that decides the design: do the rows in a cluster share one hex?
agree = sum(1 for c in multi if len({hexes.get(r['id']) for r in c if hexes.get(r['id'])}) == 1)
disagree = sum(1 for c in multi if len({hexes.get(r['id']) for r in c if hexes.get(r['id'])}) > 1)
print(f'\nclusters whose identified rows all name the same aircraft: {agree}')
print(f'clusters naming more than one aircraft: {disagree}')
