#!/usr/bin/env python3
"""Is the server actually grouping passes, and do the groups look right?"""
import collections
import json
import subprocess
import sys

DATE = sys.argv[1] if len(sys.argv) > 1 else '2026-09-21'
url = f'http://localhost:8080/api/v2/detections?queryType=all&numResults=500&date={DATE}'
rows = json.loads(subprocess.run(['curl', '-s', url], capture_output=True, text=True).stdout)['data']

events = [r for r in rows if r.get('eventDisplayName')]
grouped = collections.Counter(r['passId'] for r in events if r.get('passId'))

print(f'{len(events)} event rows')
print(f'{sum(grouped.values())} of them are in {len(grouped)} passes')
if grouped:
    sizes = sorted(grouped.values())
    print(f'pass sizes: min {sizes[0]}, median {sizes[len(sizes) // 2]}, max {sizes[-1]}')
    print(f'the list would show {len(events) - sum(grouped.values()) + len(grouped)} entries '
          f'instead of {len(events)}')

by_pass = collections.defaultdict(list)
for r in events:
    if r.get('passId'):
        by_pass[r['passId']].append(r)

for pass_id, members in list(by_pass.items())[:4]:
    members.sort(key=lambda r: r['time'])
    print(f'\npass {pass_id}: {len(members)} rows, {members[0]["time"]}-{members[-1]["time"]}')
    for r in members:
        print(f'   {r["id"]:>5} {r["time"]} {r["commonName"]:<22} {r["confidence"]:.2f} '
              f'-> {r.get("resolvedDomain") or "-"}')
