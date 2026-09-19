# Security Policy

## This is a private fork

SoundNet is a personal, non-commercial fork of
[BirdNET-Go](https://github.com/tphakala/birdnet-go). It publishes no releases and no container
images, and is not intended to be deployed by anyone other than its operator.

**If you have found a vulnerability in BirdNET-Go itself, please report it upstream**, through
the [upstream security advisories page](https://github.com/tphakala/birdnet-go/security/advisories).
Upstream fixes benefit every user; a report filed only against this fork helps nobody.

For an issue specific to code added by this fork — the diagnostics, enrichment or review-queue
layers — contact the repository owner privately. Do not open a public issue.

## Security posture of the added code

The layers SoundNet adds have two properties worth stating explicitly:

- **Outbound network calls.** The enrichment layer contacts external services (ADS-B, weather).
  These are opt-in, disabled unless configured, and must never be enabled by default. Any new
  provider making outbound requests must be documented as such.
- **Credentials.** Provider credentials (for example OpenSky OAuth2 client credentials) are read
  from an operator-supplied path or environment variable. They must never be committed, written
  into `config.yaml` under version control, or logged — including in support dumps and error
  messages.

Station coordinates are operator-entered location data. They are used to build ADS-B query
bounding boxes and must not be transmitted anywhere else.

## Supported versions

None. There are no releases. Build from source at whichever commit you intend to run.
