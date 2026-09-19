# OpenSky REST API — notes for M5 (enrichment / identity layer)

Verified against https://openskynetwork.github.io/opensky-api/rest.html on 2026-09-19.
Re-check before implementing; OpenSky changed their auth model once already.

## Getting credentials

1. Create / log in to an OpenSky Network account: https://opensky-network.org/
2. Go to the **Account** page.
3. **Create a new API client** — this yields a `client_id` and `client_secret`.

Basic auth with username/password is **no longer accepted**. OAuth2 client credentials only.

Token exchange:

    POST https://auth.opensky-network.org/auth/realms/opensky-network/protocol/openid-connect/token
    Content-Type: application/x-www-form-urlencoded
    grant_type=client_credentials&client_id=$CLIENT_ID&client_secret=$CLIENT_SECRET

Returns an `access_token`, passed as `Authorization: Bearer <token>` on every request.
Cache it and refresh only near expiry — one token manager for the whole process.

Config location: `enrichment.adsb.opensky.{client_id, client_secret}` in `config.yaml`.
Never hard-coded, never logged.

## Decisive finding: we MUST authenticate

| | Anonymous | Authenticated |
|---|---|---|
| Historical lookback | **none — `time` param is ignored** | up to **1 hour** in the past |
| Time resolution | 10 s | 5 s |
| Credits (`/states/*`) | 400 / day | 4,000 / day |

The **acoustic-lag correction in §M5 is impossible anonymously.** The whole technique is
"the aircraft was overhead N seconds before I heard it, so query the sky as it was at
`t - N`". Anonymous ignores `time` and only ever returns *now*. Authenticated access with
its 1-hour lookback and 5 s resolution is exactly what the correction needs — 5 s
resolution is comfortably finer than the lag figures involved (~9 s at 10,000 ft).

## Credits and polling budget

Quota is per-endpoint (`/states/*`, `/tracks/*`, `/flights/*` are independent buckets):

| Tier | Credits | Refill |
|---|---|---|
| Anonymous | 400 | daily |
| Standard user | 4,000 | daily |
| Active feeder (>=30% uptime/month) | 8,000 | daily |
| Licensed user | 14,400 | hourly |

`/states/all` cost depends on bounding-box area (lat range x lon range):

| Bounding box | Credits |
|---|---|
| <= 25 sq° or serial-only query | **1** |
| 25–100 sq° | 2 |
| 100–400 sq° | 3 |
| > 400 sq° or global | 4 |

A station-local bounding box is tiny, so **1 credit per call**. Budget implications:

- **M5 (runtime enrichment)** is event-driven — one call per aircraft detection. Cheap.
- **M6 (auto-labelling collector)** is the risk: it polls continuously to catch overflights.
  At 30 s intervals that is 2,880 calls/day against a 4,000 standard-user quota — it fits,
  but leaves M5 almost no headroom. Mitigations, in order of preference:
  1. Make the M6 poll interval configurable with a **credit-aware budget guard**, not a fixed timer.
  2. Track `X-Rate-Limit-Remaining` (returned on every response) as a Prometheus gauge and
     back off as it depletes; reserve a floor for M5.
  3. Feeding OpenSky data (active feeder, >=30% uptime) doubles the quota to 8,000/day.
- On exhaustion the API returns **429** with `X-Rate-Limit-Retry-After-Seconds` — honour it.

## Fields we need

From the `/states/all` state-vector array: `icao24` (0), `callsign` (1), `longitude` (5),
`latitude` (6), `baro_altitude` (7), `on_ground` (8), `velocity` (9), `true_track` (10),
`vertical_rate` (11), `geo_altitude` (13), `category` (17).

### Live verification, 2026-09-19

Credentials tested end-to-end: token exchange OK (30-minute expiry), `/states/all` returned
HTTP 200 with 78 aircraft over a test bounding box. `X-Rate-Limit-Remaining: 3999` immediately
after one call — confirming **standard tier, 4,000/day allowance**, and that a small bounding
box costs exactly **1 credit** as documented. (The header is live remaining usage, not the
allowance.)

Two findings from the real response that change the design:

1. **`category` is unreliable — do not depend on it.** Despite `extended=1`, all 78 aircraft
   returned `category = 0` ("No information at all"). The earlier idea of using ADS-B category
   as a free cross-check on the M4 jet/prop/helicopter head does **not** hold in practice: the
   field is frequently absent. Treat it as an opportunistic bonus when non-zero, never as a
   label source or a validation signal. The M4 head has to stand on its own, and M6's
   auto-labelling should key off `type`/`registration` resolved from the hex code rather than
   the category field.

2. **`geo_altitude` is frequently null** — 6 of the first 8 aircraft had none, while
   `baro_altitude` was present. Since the slant-range term needs a real altitude, the provider
   must fall back to `baro_altitude` and record which source was used in the `Enrichment` JSON.
   Barometric altitude is pressure-derived and drifts from geometric altitude, so the recorded
   source matters when assessing how much to trust the lag correction. Aircraft on the ground
   also report null altitude with zero velocity — filter `on_ground = true` out early.

## Other limits
- Flight tracks older than 30 days are unavailable.
- `/flights/*` and `/tracks/*` are batch-updated overnight — not usable for live enrichment.
- For historical work spanning more than an hour, OpenSky points at their Trino/MinIO
  interface rather than REST.
