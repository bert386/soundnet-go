#!/usr/bin/env python3
"""How often does adsb.lol actually answer us, at the rate we would ask?

"How long is the cooldown" turned out to be the wrong question: one request
returned 200 and the next 429, so it is a dynamic limit rather than a ban with
a duration. The useful question is what share of requests succeed at the
interval the station would use.

Asks at the client's reuse interval so the measurement is of the traffic we
would actually generate, not of a burst.
"""
import subprocess
import sys
import time
import urllib.request

URL = 'https://api.adsb.lol/v2/point/-34.11159/150.79226/10'
UA = 'SoundNet/1.0 (BirdNET-Go fork; acoustic event station)'
ATTEMPTS = int(sys.argv[1]) if len(sys.argv) > 1 else 10
INTERVAL = float(sys.argv[2]) if len(sys.argv) > 2 else 10.0

ok = limited = other = 0
retry_after_seen = set()

for i in range(ATTEMPTS):
    req = urllib.request.Request(URL, headers={'User-Agent': UA, 'Accept': 'application/json'})
    try:
        with urllib.request.urlopen(req, timeout=20) as resp:
            body = resp.read()
            ok += 1
            print(f'{i + 1:>3}  {resp.status}  {len(body)} bytes')
    except urllib.error.HTTPError as e:
        for header in ('Retry-After', 'X-Rate-Limit-Retry-After-Seconds', 'RateLimit-Reset'):
            if e.headers.get(header):
                retry_after_seen.add(f'{header}={e.headers.get(header)}')
        if e.code == 429:
            limited += 1
        else:
            other += 1
        print(f'{i + 1:>3}  {e.code}')
    except Exception as exc:  # noqa: BLE001 - reporting, not handling
        other += 1
        print(f'{i + 1:>3}  error: {exc}')
    if i < ATTEMPTS - 1:
        time.sleep(INTERVAL)

print(f'\n{ok} answered, {limited} rate limited, {other} other, '
      f'over {ATTEMPTS} requests {INTERVAL:.0f}s apart')
print('retry hints returned:', retry_after_seen or 'none')
