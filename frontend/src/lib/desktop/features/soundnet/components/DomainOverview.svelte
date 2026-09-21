<!--
  DomainOverview.svelte - what the station heard, grouped by what made it

  The inherited analytics are a species list, which is right for what they were
  written for. On this station they put "Vehicle", "Thunderstorm",
  "aircraft_and_airplane" and "Purr" in a count of "45 species", each with a
  bird silhouette where a photograph should be.

  So this groups by domain first and lets a domain be opened. Clicking aircraft
  shows the aircraft classes and the machines themselves; clicking vehicles
  shows the vehicle classes. The detections list is one more click away, already
  filtered, rather than reimplemented here.

  Each domain carries two numbers, because on this station they differ sharply.
  The headline is what the station concluded, after ADS-B moved the detections
  it could identify; the class list underneath is what the classifier heard, and
  is left exactly as the classifier left it. An operator tuning a threshold
  needs the second, and an operator asking what flew over needs the first.

  Class names come from the taxonomy, never the stored form. The stored names
  are truncated on the way in - "and_airscrew", "car_(siren)" - which is what
  the species page shows an operator today.
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import { navigation } from '$lib/stores/navigation.svelte';
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
    Zap,
    TriangleAlert,
  } from '@lucide/svelte';

  import AircraftCard from './AircraftCard.svelte';

  const logger = loggers.ui;

  interface ClassCount {
    label: string;
    count: number;
    maxConfidence?: number;
    lastHeard?: string;
    ambiguousWith?: string[];
  }

  interface DomainMove {
    domain: string;
    detections: number;
  }

  interface DomainSummary {
    domain: string;
    detections: number;
    heard: number;
    identifiedAs?: DomainMove[];
    identifiedFrom?: DomainMove[];
    classes: ClassCount[];
    diagnosable: boolean;
    enrichable: boolean;
    lastHeard?: string;
  }

  interface Sighting {
    hex: string;
    registration?: string;
    typeCode?: string;
    typeName?: string;
    operator?: string;
    callsign?: string;
    detections: number;
    closestKm?: number;
    firstSeen: string;
    lastSeen: string;
    sources?: string[];
  }

  interface Overview {
    from: string;
    to: string;
    domains: DomainSummary[];
    aircraft: Sighting[];
    birdDetections: number;
  }

  const PERIODS = [1, 7, 30];

  let days = $state(7);
  let data = $state<Overview | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let opened = $state<string | null>(null);

  $effect(() => {
    const period = days;
    loading = true;
    error = null;
    fetch(`/api/v2/soundnet/overview?days=${period}`)
      .then(async response => {
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
        return (await response.json()) as Overview;
      })
      .then(result => {
        data = result;
        loading = false;
      })
      .catch((err: unknown) => {
        logger.error('failed to load the event overview', err);
        error = err instanceof Error ? err.message : String(err);
        loading = false;
      });
  });

  // A domain's icon. Chosen per domain rather than per class: the point of the
  // page is that these are families, not a flat list.
  // A Map, not a plain object: the domain is a dynamic key from an API
  // response, and indexing an object with one can reach Object.prototype. The
  // same reason the detection panel reads provider attributes through a Map.
  const ICONS = new Map<string, typeof Plane>([
    ['aircraft', Plane],
    ['vehicle', Car],
    ['weather', CloudLightning],
    ['alarm', Siren],
    ['tool', Wrench],
    ['rail', Train],
    ['watercraft', Ship],
    ['music', Music],
    ['biological', Bird],
    ['impulse', Zap],
  ]);

  function iconFor(domain: string) {
    return ICONS.get(domain) ?? Zap;
  }

  const totalEvents = $derived((data?.domains ?? []).reduce((sum, d) => sum + d.detections, 0));

  function toggle(domain: string) {
    opened = opened === domain ? null : domain;
  }

  function browse(domain: string) {
    navigation.navigate(`/ui/detections?category=${encodeURIComponent(domain)}`);
  }
</script>

<div class="space-y-4">
  <div class="flex flex-wrap items-center gap-2">
    <span class="text-sm opacity-70">{t('soundnet.overview.period')}</span>
    {#each PERIODS as period (period)}
      <button
        type="button"
        class="btn btn-xs {days === period ? 'btn-primary' : 'btn-ghost'}"
        onclick={() => (days = period)}
      >
        {t('soundnet.overview.days', { count: period })}
      </button>
    {/each}
  </div>

  {#if loading}
    <div class="flex items-center gap-2 text-sm opacity-70 py-4">
      <span class="loading loading-spinner loading-sm"></span>
      {t('soundnet.overview.loading')}
    </div>
  {:else if error}
    <div role="alert" class="alert alert-error text-sm">
      <TriangleAlert class="h-4 w-4" aria-hidden="true" />
      <span>{t('soundnet.overview.loadFailed', { error })}</span>
    </div>
  {:else if data}
    <!-- Events beside birds, so the one can be read in proportion to the other. -->
    <div class="stats stats-horizontal shadow-sm w-full">
      <div class="stat py-3">
        <div class="stat-title text-xs">{t('soundnet.overview.events')}</div>
        <div class="stat-value text-2xl">{totalEvents}</div>
      </div>
      <div class="stat py-3">
        <div class="stat-title text-xs">{t('soundnet.overview.categories')}</div>
        <div class="stat-value text-2xl">{data.domains.length}</div>
      </div>
      <div class="stat py-3">
        <div class="stat-title text-xs">{t('soundnet.overview.aircraftIdentified')}</div>
        <div class="stat-value text-2xl">{data.aircraft.length}</div>
      </div>
      <div class="stat py-3">
        <div class="stat-title text-xs">{t('soundnet.overview.birds')}</div>
        <div class="stat-value text-2xl opacity-60">{data.birdDetections}</div>
      </div>
    </div>

    {#if data.domains.length === 0}
      <p class="text-sm opacity-70">{t('soundnet.overview.empty')}</p>
    {/if}

    <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
      {#each data.domains as domain (domain.domain)}
        {@const Icon = iconFor(domain.domain)}
        <div class="card bg-base-100 shadow-sm">
          <div class="card-body p-4 gap-2">
            <button
              type="button"
              class="flex items-center gap-3 text-left"
              aria-expanded={opened === domain.domain}
              onclick={() => toggle(domain.domain)}
            >
              <span class="rounded-lg bg-base-200 p-2">
                <Icon class="h-5 w-5" aria-hidden="true" />
              </span>
              <span class="flex-1">
                <span class="block font-medium capitalize">{domain.domain}</span>
                <span class="block text-xs opacity-60">
                  {t('soundnet.overview.detections', { count: domain.detections })}
                </span>
              </span>
              <span class="text-2xl font-semibold tabular-nums">{domain.detections}</span>
            </button>

            <!--
              The two halves of every correction, each shown on the card it
              changes. Without them the weather card simply reads lower than the
              classifier's own count and there is nothing on screen to say why.
            -->
            {#if domain.identifiedAs?.length}
              {#each domain.identifiedAs as move (move.domain)}
                <p class="text-xs opacity-70 flex items-center gap-1">
                  <Plane class="h-3 w-3" aria-hidden="true" />
                  {t('soundnet.overview.identifiedAs', {
                    count: move.detections,
                    domain: move.domain,
                  })}
                </p>
              {/each}
            {/if}
            {#if domain.identifiedFrom?.length}
              {#each domain.identifiedFrom as move (move.domain)}
                <p class="text-xs opacity-70">
                  {t('soundnet.overview.heardAs', {
                    count: move.detections,
                    domain: move.domain,
                  })}
                </p>
              {/each}
            {/if}

            {#if opened === domain.domain}
              <p class="text-xs opacity-50 mt-1 border-t border-base-200 pt-2">
                {t('soundnet.overview.heardHeading', { count: domain.heard })}
              </p>
              <ul class="space-y-1">
                {#each domain.classes as klass (klass.label)}
                  <li class="flex items-center gap-2 text-sm">
                    <span class="flex-1 truncate">
                      {klass.label}
                      {#if klass.ambiguousWith?.length}
                        <span class="text-xs opacity-50">
                          {t('soundnet.overview.ambiguousWith', {
                            domains: klass.ambiguousWith.join(', '),
                          })}
                        </span>
                      {/if}
                    </span>
                    {#if klass.maxConfidence}
                      <span class="font-mono text-xs opacity-50">
                        {(klass.maxConfidence * 100).toFixed(0)}%
                      </span>
                    {/if}
                    <span class="font-mono tabular-nums">{klass.count}</span>
                  </li>
                {/each}
              </ul>
              <button
                type="button"
                class="btn btn-xs btn-outline mt-2 self-start"
                onclick={() => browse(domain.domain)}
              >
                {t('soundnet.overview.browse')}
              </button>
            {/if}
          </div>
        </div>
      {/each}
    </div>

    {#if data.aircraft.length > 0}
      <div>
        <h3 class="font-medium text-sm flex items-center gap-2 mb-2">
          <Plane class="h-4 w-4" aria-hidden="true" />
          {t('soundnet.overview.aircraftHeading')}
        </h3>
        <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-3">
          {#each data.aircraft as sighting (sighting.hex)}
            <AircraftCard aircraft={sighting} />
          {/each}
        </div>
      </div>
    {/if}
  {/if}
</div>
