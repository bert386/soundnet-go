import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

import {
  ensureEventTaxonomy,
  resolveEventClass,
  eventDomainFor,
  eventTaxonomyLoaded,
} from './eventTaxonomy.svelte';
import { localizeSpeciesName } from '$lib/utils/speciesDisplay';

// The three forms the server emits, for the classes that were appearing on the
// operator's screen as "and_airscrew", "car_(siren)" and "and_eructation".
const TAXONOMY = {
  domains: [
    {
      domain: 'aircraft',
      classes: [
        {
          label: 'Propeller, airscrew',
          domain: 'aircraft',
          defaultEnabled: true,
          rawLabel: 'propeller_and_airscrew',
          storageName: 'propeller',
        },
        {
          label: 'Aircraft',
          domain: 'aircraft',
          defaultEnabled: true,
          rawLabel: 'aircraft',
          storageName: 'aircraft',
        },
      ],
    },
    {
      domain: 'alarm',
      classes: [
        {
          label: 'Police car (siren)',
          domain: 'alarm',
          defaultEnabled: true,
          rawLabel: 'police_car_(siren)',
          storageName: 'police',
        },
      ],
    },
    {
      domain: 'vehicle',
      classes: [
        {
          label: 'Car',
          domain: 'vehicle',
          defaultEnabled: true,
          rawLabel: 'car',
          storageName: 'car',
        },
        {
          label: 'Car passing by',
          domain: 'vehicle',
          defaultEnabled: true,
          rawLabel: 'car_passing_by',
          storageName: 'car',
        },
      ],
    },
  ],
};

const originalFetch = globalThis.fetch;

async function loadTaxonomy() {
  globalThis.fetch = vi.fn().mockResolvedValue({
    ok: true,
    json: () => Promise.resolve(TAXONOMY),
  }) as unknown as typeof fetch;
  ensureEventTaxonomy();
  // Let the promise chain settle.
  for (let i = 0; i < 5 && !eventTaxonomyLoaded(); i++) {
    await Promise.resolve();
  }
}

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  globalThis.fetch = originalFetch;
});

describe('event taxonomy lookup', () => {
  it('rebuilds a name from the two halves the datastore stored', async () => {
    await loadTaxonomy();

    // This is the pair as it comes out of the database: the label split at its
    // first underscore, neither half a name.
    expect(resolveEventClass('propeller', 'and_airscrew')?.label).toBe('Propeller, airscrew');
    expect(resolveEventClass('police', 'car_(siren)')?.label).toBe('Police car (siren)');
  });

  it('resolves a single-word class, where both halves are the same', async () => {
    await loadTaxonomy();

    expect(resolveEventClass('aircraft', 'Aircraft')?.label).toBe('Aircraft');
  });

  it('reports the domain, for choosing an icon over a bird silhouette', async () => {
    await loadTaxonomy();

    expect(eventDomainFor('propeller', 'and_airscrew')).toBe('aircraft');
    expect(eventDomainFor('police', 'car_(siren)')).toBe('alarm');
  });

  // A bird must never resolve, or the species pages would start renaming birds.
  it('returns null for a species', async () => {
    await loadTaxonomy();

    expect(resolveEventClass('Turdus migratorius', 'American Robin')).toBeNull();
    expect(eventDomainFor('Turdus migratorius', 'American Robin')).toBeNull();
  });

  // Several classes truncate to the same storage name. Falling back to one of
  // them is better than showing "car", and the more general reading is the
  // safer of the two when the rejoined label did not match.
  it('falls back to the truncated name when the full label does not match', async () => {
    await loadTaxonomy();

    expect(resolveEventClass('car', 'something-unexpected')?.domain).toBe('vehicle');
  });
});

describe('localizeSpeciesName with the taxonomy', () => {
  it('prefers a server-supplied event name over everything', async () => {
    await loadTaxonomy();

    expect(localizeSpeciesName('propeller', 'and_airscrew', 'Server Name')).toBe('Server Name');
  });

  // The analytics endpoints return species rows and know nothing of events, so
  // this is the path that fixes the species page, the summary and the dashboard
  // at once.
  it('names an event the analytics endpoints could not', async () => {
    await loadTaxonomy();

    expect(localizeSpeciesName('propeller', 'and_airscrew')).toBe('Propeller, airscrew');
  });

  it('leaves a bird to the species dictionary', async () => {
    await loadTaxonomy();

    const got = localizeSpeciesName('Turdus migratorius', 'American Robin');
    expect(got).toBe('American Robin');
  });
});
