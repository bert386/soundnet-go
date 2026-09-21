#!/usr/bin/env python3
"""Would the browser now resolve the names the operator saw as species?

Replays the client lookup against the live taxonomy, for the exact pairs the
screenshots showed: the stored scientific/common halves, and what the species
page should now display instead.
"""
import json
import subprocess

raw = subprocess.run(['curl', '-s', 'http://localhost:8080/api/v2/soundnet/taxonomy'],
                     capture_output=True, text=True).stdout
data = json.loads(raw)

by_raw, by_storage = {}, {}
for domain in data['domains']:
    for k in domain['classes']:
        if k.get('rawLabel'):
            by_raw[k['rawLabel']] = k
        if k.get('storageName') and k['storageName'] not in by_storage:
            by_storage[k['storageName']] = k


def resolve(sci, common):
    sci = (sci or '').strip().lower()
    common = (common or '').strip().lower()
    joined = f'{sci}_{common}' if common and common != sci else sci
    return by_raw.get(joined) or by_storage.get(sci)


# Straight from the screenshots: what was shown, and its stored pair.
CASES = [
    ('propeller', 'and_airscrew'),
    ('police', 'car_(siren)'),
    ('burping', 'and_eructation'),
    ('fixed-wing', 'aircraft_and_airplane'),
    ('vehicle', 'Vehicle'),
    ('thunderstorm', 'Thunderstorm'),
    ('aircraft', 'Aircraft'),
    ('reversing', 'beeps'),
    ('car', 'Car'),
    ('purr', 'Purr'),
    ('meow', 'Meow'),
    ('Turdus migratorius', 'American Robin'),
    ('Acridotheres tristis', 'Common Myna'),
]

print(f"{'stored':<38} {'shown now':<30} domain")
print('-' * 84)
for sci, common in CASES:
    k = resolve(sci, common)
    shown = k['label'] if k else f'(species: {common})'
    domain = k['domain'] if k else '-'
    print(f'{sci + " + " + common:<38} {shown:<30} {domain}')
