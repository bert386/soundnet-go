/**
 * The event taxonomy, client side.
 *
 * Why this exists: the station stores a non-bird detection as two halves of a
 * label split at the first underscore - "propeller" and "and_airscrew",
 * "police" and "car_(siren)", "burping" and "and_eructation". The detections
 * list gets a proper name from the server, but the analytics pages never did,
 * so an operator reads "and_airscrew" inside a count of "45 species".
 *
 * Rebuilding the name in the browser would mean reimplementing a splitter that
 * this project has already got wrong once, in a place where being wrong is
 * silent. So the server emits all three forms of every name and this does a
 * lookup. The taxonomy is a few hundred fixed entries and changes only when the
 * binary does, so it is fetched once per page load and held.
 *
 * Every lookup returns null until the fetch lands, which is the honest answer
 * and also the pre-existing behaviour: callers fall back to what they showed
 * before. Reads inside $derived re-run when it arrives.
 */

export interface EventClass {
  label: string;
  domain: string;
  defaultEnabled: boolean;
  rawLabel: string;
  storageName: string;
}

interface TaxonomyResponse {
  domains: Array<{ domain: string; classes: EventClass[] }>;
}

// Maps rather than objects: the keys are names from stored detections, and
// indexing a plain object with one can reach Object.prototype.
let byRawLabel = $state(new Map<string, EventClass>());
let byStorageName = $state(new Map<string, EventClass>());
let loaded = $state(false);
let loading = false;

/** Fetch the taxonomy once. Safe to call from anywhere, repeatedly. */
export function ensureEventTaxonomy(): void {
  if (loaded || loading) return;
  loading = true;

  fetch('/api/v2/soundnet/taxonomy')
    .then(response => (response.ok ? response.json() : null))
    .then((data: TaxonomyResponse | null) => {
      if (!data?.domains) return;
      const raw = new Map<string, EventClass>();
      const storage = new Map<string, EventClass>();
      for (const domain of data.domains) {
        for (const klass of domain.classes) {
          if (klass.rawLabel) raw.set(klass.rawLabel, klass);
          // First wins: several classes truncate to the same storage name
          // ("car" is both Car and Car passing by), and the shorter, more
          // general one is the safer thing to show when that is all we have.
          if (klass.storageName && !storage.has(klass.storageName)) {
            storage.set(klass.storageName, klass);
          }
        }
      }
      byRawLabel = raw;
      byStorageName = storage;
      loaded = true;
    })
    .catch(() => {
      // Silent. SoundNet may be off, or this may be a stock BirdNET-Go
      // server, and neither is a fault worth reporting on a bird page.
    })
    .finally(() => {
      loading = false;
    });
}

/**
 * Resolve a stored detection's name pair to its event class, or null when it is
 * a bird or the taxonomy has not arrived.
 *
 * Mirrors the server's own three-step lookup: the rejoined label first, then
 * the truncated name on its own.
 */
export function resolveEventClass(
  scientificName: string | undefined,
  commonName?: string
): EventClass | null {
  const sci = (scientificName ?? '').trim().toLowerCase();
  if (!sci || byRawLabel.size === 0) return null;

  const common = (commonName ?? '').trim().toLowerCase();
  const joined = common && common !== sci ? `${sci}_${common}` : sci;

  return byRawLabel.get(joined) ?? byStorageName.get(sci) ?? null;
}

/** The domain a stored detection belongs to, or null for a bird. */
export function eventDomainFor(
  scientificName: string | undefined,
  commonName?: string
): string | null {
  return resolveEventClass(scientificName, commonName)?.domain ?? null;
}

/** Whether the taxonomy has arrived, for callers that want to wait. */
export function eventTaxonomyLoaded(): boolean {
  return loaded;
}
