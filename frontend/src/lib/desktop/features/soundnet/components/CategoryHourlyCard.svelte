<!--
  CategoryHourlyCard.svelte - the day by hour, grouped by what made the sound

  The daily summary this fork inherited gives every species its own row of
  twenty-four hours, which is the right shape for a bird station and the wrong
  one here: forty rows of birds, then one row each for Vehicle, Thunderstorm and
  Propeller, all in the same list and all called species. The shape of the day -
  dawn chorus here, morning traffic there, the 06:40 flight every weekday - is
  in that table and unreadable.

  So: one row per family at the top, the birds collapsed into a single row, and
  then a grid of its own for each family showing the classes inside it.

  The top grid reports what the station concluded, not what the classifier
  heard. ADS-B identifies an aeroplane on a great many rows recorded as
  `Thunderstorm` or `Vehicle`, and the overview endpoint already rebases its
  totals on that; an hour-by-hour view that did not would file the same
  detection under weather while the card beside it filed it under aircraft. So
  this asks for the same corrections split by hour and applies them. The family
  grids below stay exactly as the classifier left them, because that is the
  other half of the question and an operator tuning a threshold needs it.

  It deliberately looks like the summary it sits above. The heatmap colour
  classes are upstream's and are declared `:global`, so the cells here are the
  same cells; only the custom properties they read have to be repeated, because
  upstream defines those on its own card and Svelte scopes them to it. If that
  palette ever changes, change it here too - the class names will not warn you,
  they will just render the old colours.

  The counts come from the daily summary the species table already fetched, so
  the only request this adds is the one for the corrections.
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import { navigation } from '$lib/stores/navigation.svelte';
  import type { DailySpeciesSummary } from '$lib/types/detection.types';
  import { ensureEventTaxonomy, resolveEventClass } from '$lib/stores/eventTaxonomy.svelte';
  import {
    Plane,
    Car,
    CloudLightning,
    Siren,
    Wrench,
    Train,
    Ship,
    Music,
    Bird,
    Cat,
    Zap,
  } from '@lucide/svelte';

  interface Props {
    data: DailySpeciesSummary[];
    date: string;
  }

  let { data, date }: Props = $props();

  ensureEventTaxonomy();

  const HOURS = 24;
  const HOUR_LIST = Array.from({ length: HOURS }, (_, hour) => hour);

  // The same fixed scale upstream's summary uses, so a cell of a given colour
  // means the same number of detections in both grids. A per-grid scale would
  // read better inside a quiet category and would make the two tables
  // incomparable, which is worse.
  const MAX_HEAT_COUNT = 50;
  const INTENSITY_LEVELS = 9;

  function intensity(count: number): number {
    if (count <= 0) return 0;
    const step = MAX_HEAT_COUNT / INTENSITY_LEVELS;
    return Math.min(INTENSITY_LEVELS, Math.max(1, Math.ceil(count / step)));
  }

  interface HourlyMove {
    hour: number;
    from: string;
    to: string;
    detections: number;
  }

  let moves = $state<HourlyMove[]>([]);

  interface HourlyEngine {
    hour: number;
    engine: string;
    detections: number;
  }

  // Jet / prop / helicopter from ADS-B's type codes. Empty on a station with no
  // identifications, in which case the aircraft grid keeps the classifier's rows.
  let engines = $state<HourlyEngine[]>([]);
  const ENGINE_ORDER = ['jet', 'prop', 'helicopter', 'other', 'unidentified'];

  $effect(() => {
    const day = date;
    if (!day) return;

    let current = true;
    fetch(`/api/v2/soundnet/overview?date=${encodeURIComponent(day)}`)
      .then(response => (response.ok ? response.json() : null))
      .then((result: { hourlyMoves?: HourlyMove[]; hourlyEngines?: HourlyEngine[] } | null) => {
        if (!current) return;
        moves = result?.hourlyMoves ?? [];
        engines = result?.hourlyEngines ?? [];
      })
      .catch(() => {
        // Silent, and empty. SoundNet may be off, or this may be a stock
        // BirdNET-Go server; in either case the acoustic reading below is still
        // a true answer, just an uncorrected one.
        if (current) {
          moves = [];
          engines = [];
        }
      });

    return () => {
      current = false;
    };
  });

  // The birds are one row, and that row has no grid of its own: the species
  // table below is where a bird belongs, and thirty more rows here would
  // recreate exactly the list this card exists to collapse.
  const BIRDS = 'birds';

  interface Row {
    key: string;
    label: string;
    /** The name this class is stored under, which is what the list filters on. */
    storageName?: string;
    /** For an engine-class row: jet, prop, helicopter, other, unidentified. */
    engine?: string;
    total: number;
    /** What the classifier said, before any identification moved anything. */
    heard: number;
    hours: number[];
    children: Row[];
    /** Where detections went, and where they came from, by domain. */
    movedTo: Map<string, number>;
    movedFrom: Map<string, number>;
  }

  function emptyHours(): number[] {
    return new Array<number>(HOURS).fill(0);
  }

  function newRow(key: string, label: string, storageName?: string): Row {
    return {
      key,
      label,
      storageName,
      total: 0,
      heard: 0,
      hours: emptyHours(),
      children: [],
      movedTo: new Map(),
      movedFrom: new Map(),
    };
  }

  function addInto(hours: number[], counts: number[] | undefined) {
    if (!counts) return;
    for (let hour = 0; hour < HOURS; hour++) {
      // `hour` is a loop counter bounded by HOURS, not a key that came from a
      // detection, and both sides are fixed-length local number arrays.
      // eslint-disable-next-line security/detect-object-injection
      hours[hour] += counts[hour] ?? 0;
    }
  }

  function bump(tally: Map<string, number>, key: string, by: number) {
    tally.set(key, (tally.get(key) ?? 0) + by);
  }

  // Maps rather than plain objects throughout: every key here is a domain or a
  // class name that arrived from the database, and indexing an object with one
  // can reach Object.prototype.
  const rows = $derived.by<Row[]>(() => {
    const byDomain = new Map<string, Row>();
    const birds = newRow(BIRDS, t('soundnet.hourly.birds'));

    for (const entry of data) {
      const klass = resolveEventClass(entry.scientific_name, entry.common_name);
      if (!klass) {
        birds.total += entry.count;
        birds.heard += entry.count;
        addInto(birds.hours, entry.hourly_counts);
        continue;
      }

      let domain = byDomain.get(klass.domain);
      if (!domain) {
        domain = newRow(klass.domain, klass.domain);
        byDomain.set(klass.domain, domain);
      }
      domain.total += entry.count;
      domain.heard += entry.count;
      addInto(domain.hours, entry.hourly_counts);

      // Two stored names can resolve to one class, so they are merged rather
      // than listed twice.
      let child = domain.children.find(c => c.key === klass.label);
      if (!child) {
        child = newRow(klass.label, klass.label, klass.storageName);
        domain.children.push(child);
      }
      child.total += entry.count;
      child.heard += entry.count;
      addInto(child.hours, entry.hourly_counts);
    }

    // The corrections, hour by hour. A move can only take detections the hour
    // actually has: the enrichment window and the summary's day are not the
    // same query, and an hour that reported negative weather would be worse
    // than one that under-corrects.
    for (const move of moves) {
      if (!Number.isInteger(move.hour) || move.hour < 0 || move.hour >= HOURS) continue;
      const source = byDomain.get(move.from);
      if (!source) continue;

      // move.hour is bounds-checked against HOURS just above, so every index
      // below is a validated integer into a fixed-length local array.
      const moved = Math.min(move.detections, source.hours[move.hour] ?? 0);
      if (moved <= 0) continue;

      source.hours[move.hour] -= moved;
      source.total -= moved;
      bump(source.movedTo, move.to, moved);

      let target = byDomain.get(move.to);
      if (!target) {
        target = newRow(move.to, move.to);
        byDomain.set(move.to, target);
      }
      target.hours[move.hour] += moved;
      target.total += moved;
      bump(target.movedFrom, move.from, moved);
    }

    // The aircraft grid by engine class rather than by the classifier's
    // Aircraft / Fixed-wing / Propeller, which record how the model guessed
    // rather than what flew. Unidentified is the remainder of the concluded
    // aircraft row, so the grid's rows always add up to the row above them.
    const aircraft = byDomain.get('aircraft');
    if (aircraft && engines.length > 0) {
      const byEngine = new Map<string, Row>();
      for (const engine of ENGINE_ORDER) {
        const row = newRow(`engine:${engine}`, engineLabel(engine));
        row.engine = engine;
        byEngine.set(engine, row);
      }
      const identified = emptyHours();
      for (const e of engines) {
        if (!Number.isInteger(e.hour) || e.hour < 0 || e.hour >= HOURS) continue;
        const row = byEngine.get(e.engine) ?? byEngine.get('other');
        if (!row) continue;
        row.hours[e.hour] += e.detections;
        row.total += e.detections;
        identified[e.hour] += e.detections;
      }
      const unidentified = byEngine.get('unidentified');
      if (unidentified) {
        for (let hour = 0; hour < HOURS; hour++) {
          // `hour` is a loop counter bounded by HOURS over fixed-length locals.
          /* eslint-disable security/detect-object-injection */
          const rest = Math.max(0, aircraft.hours[hour] - identified[hour]);
          unidentified.hours[hour] = rest;
          /* eslint-enable security/detect-object-injection */
          unidentified.total += rest;
        }
      }
      aircraft.children = ENGINE_ORDER.map(e => byEngine.get(e)).filter(
        (r): r is Row => r !== undefined && r.total > 0
      );
    }

    const out = [...byDomain.values()].filter(row => row.total > 0 || row.heard > 0);
    for (const domain of out) {
      domain.children.sort((a, b) => b.total - a.total);
    }
    out.sort((a, b) => b.total - a.total);
    if (birds.total > 0) out.push(birds);
    return out;
  });

  // The families that get a grid of their own: everything except the birds.
  const families = $derived(rows.filter(row => row.key !== BIRDS && row.children.length > 0));

  const ICONS = new Map<string, typeof Plane>([
    ['aircraft', Plane],
    ['vehicle', Car],
    ['weather', CloudLightning],
    ['alarm', Siren],
    ['tool', Wrench],
    ['rail', Train],
    ['watercraft', Ship],
    ['music', Music],
    [BIRDS, Bird],
    ['biological', Cat],
    ['impulse', Zap],
  ]);

  function iconFor(key: string) {
    return ICONS.get(key) ?? Zap;
  }

  // Every cell is a link into the list, filtered to exactly what it counts.
  //
  // The parameters were checked against the running station rather than read
  // off the handler: `category`, `date`, `hour` and `species` do compose, which
  // was not obvious - `hour` is the hourly handler's parameter and `category`
  // forces the advanced path.
  //
  // Species is filtered on the stored name, not the display name. "Propeller,
  // airscrew" is stored as "propeller", and a link built from the label would
  // land on an empty list.
  // Written out rather than built from the engine name: a computed
  // translation key cannot be checked, and a typo would render the raw key.
  function engineLabel(engine: string): string {
    switch (engine) {
      case 'jet':
        return t('soundnet.hourly.engine.jet');
      case 'prop':
        return t('soundnet.hourly.engine.prop');
      case 'helicopter':
        return t('soundnet.hourly.engine.helicopter');
      case 'unidentified':
        return t('soundnet.hourly.engine.unidentified');
      default:
        return t('soundnet.hourly.engine.other');
    }
  }

  function listUrl(
    domain: string,
    opts: { hour?: number; storageName?: string; engine?: string } = {}
  ): string {
    const query = new URLSearchParams({ queryType: 'all', category: domain, date });
    if (opts.hour !== undefined) query.set('hour', String(opts.hour));
    if (opts.storageName) query.set('species', opts.storageName);
    if (opts.engine) query.set('engine', opts.engine);
    return `/ui/detections?${query.toString()}`;
  }

  function browse(key: string) {
    if (key === BIRDS) return;
    navigation.navigate(listUrl(key));
  }

  function openCell(row: Row, domain: string, hour: number, count: number) {
    if (count <= 0 || domain === BIRDS) return;
    navigation.navigate(
      listUrl(domain, { hour, storageName: row.storageName, engine: row.engine })
    );
  }

  function cellTitle(label: string, hour: number, count: number): string {
    return `${label} · ${String(hour).padStart(2, '0')}:00 · ${count}`;
  }

  function movedSummary(row: Row): string {
    const parts: string[] = [];
    for (const [domain, count] of row.movedTo) {
      parts.push(t('soundnet.overview.identifiedAs', { count, domain }));
    }
    for (const [domain, count] of row.movedFrom) {
      parts.push(t('soundnet.overview.heardAs', { count, domain }));
    }
    return parts.join(' · ');
  }
</script>

{#snippet hourHeader()}
  <div class="hourly-row">
    <div class="label-col"></div>
    <div class="hourly-grid">
      {#each HOUR_LIST as hour (hour)}
        <div class="hour-label">{String(hour).padStart(2, '0')}</div>
      {/each}
    </div>
  </div>
{/snippet}

{#snippet heatRow(row: Row, clickable: boolean, domain: string)}
  <div class="hourly-row">
    <div class="label-col">
      {#if clickable}
        {@const Icon = iconFor(row.key)}
        <button type="button" class="row-label" onclick={() => browse(row.key)}>
          <Icon class="size-4 shrink-0 opacity-70" aria-hidden="true" />
          <span class="capitalize truncate">{row.label}</span>
          <span class="row-total">{row.total}</span>
        </button>
      {:else}
        <span class="row-label">
          <span class="truncate">{row.label}</span>
          <span class="row-total">{row.total}</span>
        </span>
      {/if}
    </div>
    <div class="hourly-grid">
      {#each row.hours as count, hour (hour)}
        {#if count > 0 && domain !== BIRDS}
          <button
            type="button"
            class="heat-cell heatmap-color-{intensity(count)}"
            title={cellTitle(row.label, hour, count)}
            onclick={() => openCell(row, domain, hour, count)}
          >
            {count}
          </button>
        {:else}
          <div
            class="heat-cell heatmap-color-{intensity(count)}"
            title={cellTitle(row.label, hour, count)}
          >
            {count || ''}
          </div>
        {/if}
      {/each}
    </div>
  </div>
{/snippet}

{#if rows.length > 0}
  <section class="soundnet-hourly card col-span-12 bg-base-100 shadow-sm">
    <div class="card-body p-4 sm:p-6 gap-3">
      <div>
        <h2 class="card-title text-base">{t('soundnet.hourly.title')}</h2>
        <p class="text-sm opacity-70">{t('soundnet.hourly.intro')}</p>
      </div>

      <div class="grid-block">
        {@render hourHeader()}
        {#each rows as row (row.key)}
          {@render heatRow(row, true, row.key)}
        {/each}
      </div>

      <!--
        One grid per family, which is the part a category view is actually for:
        aircraft splits into Aircraft, Fixed-wing, Propeller and Helicopter, and
        the shape of each is different.
      -->
      {#each families as family (family.key)}
        {@const Icon = iconFor(family.key)}
        {@const moved = movedSummary(family)}
        <div class="grid-block">
          <h3 class="family-heading">
            <Icon class="size-4 opacity-70" aria-hidden="true" />
            <span class="capitalize font-medium">{family.label}</span>
            <span class="opacity-50 text-xs">
              {t('soundnet.hourly.heard', { count: family.heard })}
              {#if moved}· {moved}{/if}
            </span>
          </h3>
          {@render hourHeader()}
          {#each family.children as child (child.key)}
            {@render heatRow(child, false, family.key)}
          {/each}
        </div>
      {/each}

      <div class="flex items-center justify-between gap-2 text-xs opacity-60">
        <span>{t('soundnet.hourly.footnote')}</span>
        <span class="flex items-center gap-1">
          {t('soundnet.hourly.less')}
          {#each [0, 1, 2, 3, 4, 5, 6, 7, 8, 9] as level (level)}
            <span class="legend-swatch heatmap-color-{level}"></span>
          {/each}
          {t('soundnet.hourly.more')}
        </span>
      </div>
    </div>
  </section>
{/if}

<style>
  /*
    The custom properties upstream's `:global(.heatmap-color-N)` rules read.
    They are defined on the daily summary's own card and Svelte scopes them
    there, so they have to be repeated rather than inherited. The rules
    themselves are shared, which is what keeps the two grids looking alike.
  */
  .soundnet-hourly {
    --grid-cell-radius: 4px;
    --grid-gap: 4px;
    --heatmap-color-0: #f0f9fc;
    --heatmap-color-1: #e0f3f8;
    --heatmap-color-2: #ccebf6;
    --heatmap-color-3: #99d7ed;
    --heatmap-color-4: #66c2e4;
    --heatmap-color-5: #33ade1;
    --heatmap-color-6: #0099d8;
    --heatmap-color-7: #0077be;
    --heatmap-color-8: #005595;
    --heatmap-color-9: #036;
  }

  :global([data-theme='dark']) .soundnet-hourly {
    --heatmap-color-0: #1e293b;
    --heatmap-color-1: #164e63;
    --heatmap-color-2: #0e7490;
    --heatmap-color-3: #0891b2;
    --heatmap-color-4: #06b6d4;
    --heatmap-color-5: #22d3ee;
    --heatmap-color-6: #38bdf8;
    --heatmap-color-7: #60a5fa;
    --heatmap-color-8: #93c5fd;
    --heatmap-color-9: #bfdbfe;
  }

  .grid-block {
    display: flex;
    flex-direction: column;
    gap: var(--grid-gap);
  }

  .family-heading {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    margin-top: 0.5rem;
    padding-top: 0.5rem;
    border-top: 1px solid var(--color-base-200);
    font-size: 0.875rem;
  }

  /* Label column plus twenty-four equal hours, so the whole day fits the card
     without a horizontal scrollbar. */
  .hourly-row {
    display: grid;
    grid-template-columns: var(--label-col-width, 10rem) minmax(0, 1fr);
    gap: var(--grid-gap);
    align-items: center;
  }

  .hourly-grid {
    display: grid;
    grid-template-columns: repeat(24, minmax(0, 1fr));
    gap: var(--grid-gap);
  }

  .label-col {
    min-width: 0;
  }

  .row-label {
    display: flex;
    align-items: center;
    gap: 0.375rem;
    width: 100%;
    min-width: 0;
    font-size: 0.8125rem;
    text-align: left;
  }

  button.row-label:hover span:not(.row-total) {
    text-decoration: underline;
  }

  .row-total {
    margin-left: auto;
    font-variant-numeric: tabular-nums;
    font-family: ui-monospace, monospace;
    font-size: 0.75rem;
    opacity: 0.7;
  }

  .hour-label {
    text-align: center;
    font-family: ui-monospace, monospace;
    font-size: 0.625rem;
    opacity: 0.5;
  }

  button.heat-cell {
    cursor: pointer;
  }

  button.heat-cell:hover {
    outline: 2px solid var(--color-base-content);
    outline-offset: -2px;
  }

  .heat-cell {
    height: 1.25rem;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 0.625rem;
    font-weight: 500;
    font-variant-numeric: tabular-nums;
    overflow: hidden;
  }

  .legend-swatch {
    width: 0.75rem;
    height: 0.75rem;
    display: inline-block;
  }

  /* Narrow screens: the counts inside the cells are the first thing worth
     losing, and a shorter label column keeps twenty-four cells readable. */
  @media (max-width: 1024px) {
    .hourly-row {
      --label-col-width: 7.5rem;
    }

    .hour-label {
      font-size: 0.5rem;
    }

    .heat-cell {
      font-size: 0;
    }
  }
</style>
