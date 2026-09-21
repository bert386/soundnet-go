#!/usr/bin/env python3
"""The operator's thunder reviews.

They say they left the ones tagged "identified as aircraft" alone and reviewed
only the ones that sounded like real thunder. So the interesting question is not
how many are false positives - it is whether any survived as genuine, and what
those look like compared with the jets.
"""
import json
import subprocess

BASE = 'http://localhost:8080/api/v2'
rows = {}
for date in ('2026-09-21', '2026-09-20'):
    url = f'{BASE}/detections?queryType=all&numResults=500&date={date}'
    raw = subprocess.run(['curl', '-s', url], capture_output=True, text=True).stdout
    if raw.strip():
        for r in json.loads(raw)['data']:
            rows[r['id']] = r

storm = [r for r in rows.values()
         if (r.get('eventDisplayName') or '') in ('Thunder', 'Thunderstorm')]
storm.sort(key=lambda r: (r['date'], r['time']))

reviewed = [r for r in storm if r.get('verified', 'unverified') != 'unverified']
print(f'{len(storm)} thunder/thunderstorm rows, {len(reviewed)} reviewed\n')

for r in storm:
    verified = r.get('verified', 'unverified')
    comments = r.get('comments') or []
    texts = [c.get('entry') or c.get('text') or '' for c in comments]
    texts = [t.strip() for t in texts if t and t.strip()]
    mark = {'correct': 'REAL ', 'false_positive': 'false', 'unverified': '  -  '}.get(verified, verified)
    print(f"{mark} {r['id']:>5} {r['date']} {r['time']} {r['eventDisplayName']:<13} "
          f"{r['confidence']:.2f} resolved={r.get('resolvedDomain') or '-':<9} "
          f"pass={r.get('passId') or '-'}")
    for t in texts:
        print(f"         note: {t}")

real = [r for r in storm if r.get('verified') == 'correct']
false = [r for r in storm if r.get('verified') == 'false_positive']
print(f'\nconfirmed real thunder: {len(real)}')
print(f'confirmed false:        {len(false)}')
if real:
    print('  real -> ' + ', '.join(f"{r['id']}@{r['confidence']:.2f}" for r in real))
if false:
    resolved = sum(1 for r in false if r.get('resolvedDomain'))
    print(f'  of the false ones, {resolved} had already been resolved to another domain')
