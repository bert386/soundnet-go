#!/usr/bin/env python3
"""Does the new overview endpoint answer, and with the taxonomy's names?"""
import json
import subprocess

raw = subprocess.run(
    ['curl', '-s', 'http://localhost:8080/api/v2/soundnet/overview?days=7'],
    capture_output=True, text=True).stdout
d = json.loads(raw)

print(f"period {d['from'][:16]} to {d['to'][:16]}")
print(f"bird detections: {d['birdDetections']}")
print(f"aircraft identified: {len(d['aircraft'])}\n")

for dom in d['domains']:
    top = ', '.join(f"{c['label']} {c['count']}" for c in dom['classes'][:4])
    print(f"{dom['domain']:<12} {dom['detections']:>5}   {top}")

print()
for a in d['aircraft'][:10]:
    print(f"  {a['hex']} {a.get('registration', '-'):<8} {a.get('typeCode', '-'):<6} "
          f"{a['detections']:>3} det  closest {a.get('closestKm', 0):.1f} km  "
          f"src={','.join(a.get('sources') or [])}")

mangled = [c['label'] for dom in d['domains'] for c in dom['classes'] if '_' in c['label']]
print('\nmangled labels still shown:', mangled or 'none')
