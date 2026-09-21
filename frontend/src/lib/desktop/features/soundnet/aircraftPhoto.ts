/**
 * Photographs and outside links for an identified aircraft.
 *
 * Extracted from the events page because the same aircraft now appears in three
 * places - the events overview, the dashboard and a detection's own panel - and
 * the photograph is fetched from someone else's service under terms that are
 * easy to breach by accident. One copy of the rules is auditable; three are not.
 *
 * The photograph is fetched **in the browser, never proxied through our API**.
 * Planespotters' terms ask for that, and for attribution with a link back,
 * which is why the photographer's name travels with the image rather than being
 * dropped as decoration.
 *
 * A miss is an ordinary outcome, not a fault: military, private and newly
 * registered aircraft are often absent, and their service answers 5xx often
 * enough that a station showing an error for it would cry wolf. Every failure
 * resolves to null and the caller shows the silhouette it would have shown
 * anyway.
 */

export interface AircraftPhoto {
  src: string;
  link: string;
  photographer: string;
}

// One in-memory cache per page load, keyed on the hex code.
//
// Without it the dashboard, the events page and a detail panel each ask for the
// same aircraft, and a strip of twelve cards asks twelve times on every rerender
// - which is how the ADS-B client got itself rate limited within minutes of
// going live. A null is cached as deliberately as a photograph: an aircraft with
// no picture today will not have one in the next thirty seconds either.
const cache = new Map<string, Promise<AircraftPhoto | null>>();

/** A photograph of this aircraft, or null when there is none to show. */
export function aircraftPhoto(hex: string | null | undefined): Promise<AircraftPhoto | null> {
  const key = (hex ?? '').trim().toLowerCase();
  if (!key) return Promise.resolve(null);

  const cached = cache.get(key);
  if (cached) return cached;

  const pending = fetch(`https://api.planespotters.net/pub/photos/hex/${encodeURIComponent(key)}`)
    .then(response => (response.ok ? response.json() : null))
    .then((data: { photos?: Array<Record<string, unknown>> } | null) => {
      const first = data?.photos?.[0];
      if (!first) return null;
      const thumb = (first.thumbnail_large ?? first.thumbnail) as { src?: string } | undefined;
      if (!thumb?.src) return null;
      return {
        src: thumb.src,
        link: typeof first.link === 'string' ? first.link : '',
        photographer: typeof first.photographer === 'string' ? first.photographer : '',
      };
    })
    .catch(() => null);

  cache.set(key, pending);
  return pending;
}

/**
 * Where to watch this aircraft fly.
 *
 * Keyed on the hex code, which is what the aircraft itself broadcast and so the
 * one identifier that always resolves. Registration is a lookup and is often
 * missing.
 */
export function flightPathUrl(hex: string): string {
  return `https://globe.adsb.lol/?icao=${encodeURIComponent(hex)}`;
}

/** The aircraft's record, when a registration lookup succeeded. */
export function aircraftRecordUrl(registration: string | undefined): string {
  if (!registration) return '';
  return `https://www.flightradar24.com/data/aircraft/${encodeURIComponent(
    registration.toLowerCase()
  )}`;
}
