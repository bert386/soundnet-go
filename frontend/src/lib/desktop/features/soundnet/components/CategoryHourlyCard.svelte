<!--
  CategoryHourlyCard.svelte - the day by hour, grouped by what made the sound

  The daily summary this fork inherited gives every species its own row of
  twenty-four hours, which is the right shape for a bird station and the wrong
  one here: forty rows of birds, then one row each for Vehicle, Thunderstorm and
  Propeller, all in the same list and all called species. The shape of the day -
  dawn chorus here, morning traffic there, the 06:40 flight every weekday - is
  in that table and unreadable.

  So this collapses the birds into one row and gives every event family its own,
  with the classes underneath when a row is opened. Aircraft opens to Aircraft,
  Fixed-wing, Propeller and Helicopter; vehicles to Car, Truck and the rest.

  It reads the same daily summary the species table already fetched and groups
  it in the browser, so it costs no extra request and cannot disagree with the
  table above it. The grouping is the taxonomy's, looked up rather than rebuilt:
  see lib/stores/eventTaxonomy.
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
    Zap,
    ChevronRight,
  } from '@lucide/svelte';

  interface Props {
    data: DailySpeciesSummary[];
    date: string;
  }

  let { data, date }: Props = $props();

  ensureEventTaxonomy();

  const HOURS = 24;

  // The birds are one row, and the row is not openable: the species table below
  // is where a bird belongs, and duplicating forty rows here would recreate
  // exactly the list this card exists to collapse.
  const BIRDS = 'birds';

  interface Row {
    key: string;
    label: string;
    total: number;
    hours: number[];
    children: Row[];
  }

  function emptyHours(): number[] {
    return new Array<number>(HOURS).fill(0);
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

  // Maps rather than plain objects throughout: every key here is a domain or a
  // class name that arrived from the database, and indexing an object with one
  // can reach Object.prototype.
  const rows = $derived.by<Row[]>(() => {
    const byDomain = new Map<string, Row>();
    const birds: Row = {
      key: BIRDS,
      label: t('soundnet.hourly.birds'),
      total: 0,
      hours: emptyHours(),
      children: [],
    };

    for (const entry of data) {
      const klass = resolveEventClass(entry.scientific_name, entry.common_name);
      if (!klass) {
        birds.total += entry.count;
        addInto(birds.hours, entry.hourly_counts);
        continue;
      }

      let domain = byDomain.get(klass.domain);
      if (!domain) {
        domain = {
          key: klass.domain,
          label: klass.domain,
          total: 0,
          hours: emptyHours(),
          children: [],
        };
        byDomain.set(klass.domain, domain);
      }
      domain.total += entry.count;
      addInto(domain.hours, entry.hourly_counts);

      // The class rows, which are what the operator opens a category to see.
      // Two stored names can resolve to one class, so they are merged rather
      // than listed twice.
      let child = domain.children.find(c => c.key === klass.label);
      if (!child) {
        child = {
          key: klass.label,
          label: klass.label,
          total: 0,
          hours: emptyHours(),
          children: [],
        };
        domain.children.push(child);
      }
      child.total += entry.count;
      addInto(child.hours, entry.hourly_counts);
    }

    const out = [...byDomain.values()];
    for (const domain of out) {
      domain.children.sort((a, b) => b.total - a.total);
    }
    out.sort((a, b) => b.total - a.total);
    if (birds.total > 0) out.push(birds);
    return out;
  });

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
    ['biological', Bird],
    ['impulse', Zap],
  ]);

  function iconFor(key: string) {
    return ICONS.get(key) ?? Zap;
  }

  let opened = $state<string | null>(null);

  function toggle(row: Row) {
    if (row.children.length === 0) return;
    opened = opened === row.key ? null : row.key;
  }

  function browse(row: Row) {
    if (row.key === BIRDS) return;
    navigation.navigate(`/ui/detections?category=${encodeURIComponent(row.key)}`);
  }

  // Shading is per row, not across the card. One busy category would otherwise
  // flatten every other row to nothing, which is the opposite of what a
  // time-of-day view is for: the shape of a quiet category matters as much as
  // the shape of a loud one.
  function shade(count: number, peak: number): string {
    if (count === 0) return 'opacity-0';
    const share = peak > 0 ? count / peak : 0;
    if (share > 0.66) return 'opacity-100';
    if (share > 0.33) return 'opacity-70';
    return 'opacity-40';
  }
</script>

{#if rows.length > 0}
  <section class="card bg-base-100 shadow-sm col-span-12">
    <div class="card-body p-4 sm:p-6">
      <h2 class="card-title text-base">{t('soundnet.hourly.title')}</h2>
      <p class="text-sm opacity-70">{t('soundnet.hourly.intro')}</p>

      <div class="overflow-x-auto mt-3">
        <table class="table table-xs w-full">
          <thead>
            <tr>
              <th class="text-left w-48">{t('soundnet.hourly.category')}</th>
              <th class="text-right w-12">{t('soundnet.hourly.total')}</th>
              {#each Array.from({ length: HOURS }, (_, hour) => hour) as hour (hour)}
                <th class="text-center font-mono font-normal opacity-50 px-0">
                  {String(hour).padStart(2, '0')}
                </th>
              {/each}
            </tr>
          </thead>
          <tbody>
            {#each rows as row (row.key)}
              {@const peak = Math.max(...row.hours)}
              {@const Icon = iconFor(row.key)}
              <tr class="hover">
                <td class="w-48">
                  <button
                    type="button"
                    class="flex items-center gap-2 text-left w-full"
                    aria-expanded={opened === row.key}
                    onclick={() => toggle(row)}
                  >
                    {#if row.children.length > 0}
                      <ChevronRight
                        class="h-3 w-3 shrink-0 transition-transform {opened === row.key
                          ? 'rotate-90'
                          : ''}"
                        aria-hidden="true"
                      />
                    {:else}
                      <span class="w-3 shrink-0"></span>
                    {/if}
                    <Icon class="h-4 w-4 shrink-0 opacity-70" aria-hidden="true" />
                    <span class="capitalize truncate">{row.label}</span>
                  </button>
                </td>
                <td class="text-right font-mono">
                  {#if row.key === BIRDS}
                    {row.total}
                  {:else}
                    <button type="button" class="hover:underline" onclick={() => browse(row)}>
                      {row.total}
                    </button>
                  {/if}
                </td>
                {#each row.hours as count, hour (hour)}
                  <td class="text-center font-mono px-0 {shade(count, peak)}">{count || ''}</td>
                {/each}
              </tr>

              {#if opened === row.key}
                {#each row.children as child (child.key)}
                  {@const childPeak = Math.max(...child.hours)}
                  <tr class="text-xs opacity-80">
                    <td class="w-48 pl-9 truncate">{child.label}</td>
                    <td class="text-right font-mono">{child.total}</td>
                    {#each child.hours as count, hour (hour)}
                      <td class="text-center font-mono px-0 {shade(count, childPeak)}">
                        {count || ''}
                      </td>
                    {/each}
                  </tr>
                {/each}
              {/if}
            {/each}
          </tbody>
        </table>
      </div>

      <p class="text-xs opacity-50 mt-2">{t('soundnet.hourly.footnote', { date })}</p>
    </div>
  </section>
{/if}
