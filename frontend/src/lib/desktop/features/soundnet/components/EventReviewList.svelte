<!--
  EventReviewList.svelte - unreviewed non-bird detections, one per pass

  Purpose: find what still needs a human decision, and hand it to the review
  BirdNET-Go already has.

  That review is better than anything bespoke would be. It shows the
  spectrogram, which is most of what makes a jet distinguishable from a thunder
  clap by eye, and it is the one the operator already uses. So this list does
  not review anything itself; it only decides what is worth reviewing.

  Two things it leaves out, both on the operator's word:
  - Birds. The species models are mature and the station has a review flow for
    them already; this is for the events the new models are still learning.
  - All but one row of a pass. One aeroplane makes three to six detections in
    half a minute, and reviewing it six times teaches nothing the first review
    did not.
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import { navigation } from '$lib/stores/navigation.svelte';
  import type { Detection } from '$lib/types/detection.types';
  import { ChevronRight, CircleCheck, TriangleAlert } from '@lucide/svelte';

  const logger = loggers.ui;

  // How far back to look. The recent endpoint is newest-first and unfiltered,
  // so this bounds a page that birds will mostly fill.
  const LOOKBACK = 500;

  let rows = $state<Detection[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);

  $effect(() => {
    loading = true;
    error = null;
    fetch(`/api/v2/detections/recent?limit=${LOOKBACK}`)
      .then(async response => {
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
        return (await response.json()) as Detection[];
      })
      .then(all => {
        rows = pendingEvents(all);
        loading = false;
      })
      .catch((err: unknown) => {
        // Surfaced: an empty review list and a failed one must not look alike.
        logger.error('failed to load events awaiting review', err);
        error = err instanceof Error ? err.message : String(err);
        loading = false;
      });
  });

  // Unreviewed event detections, one per pass, newest first.
  function pendingEvents(all: Detection[]): Detection[] {
    const events = all.filter(d => d.eventDisplayName && d.verified === 'unverified');

    const byPass = new Map<number, Detection[]>();
    const loose: Detection[] = [];
    for (const d of events) {
      if (!d.passId) {
        loose.push(d);
        continue;
      }
      const members = byPass.get(d.passId);
      if (members) members.push(d);
      else byPass.set(d.passId, [d]);
    }

    // A row whose own class already names the right thing is preferred over one
    // an authority had to correct, for the same reason as in the detections
    // list: the loudest row of an aircraft pass is usually "Vehicle".
    const representatives = [...byPass.values()].map(members => {
      const uncorrected = members.filter(d => !d.resolvedDomain);
      const pool = uncorrected.length > 0 ? uncorrected : members;
      return pool.reduce((best, d) => (d.confidence > best.confidence ? d : best));
    });

    return [...loose, ...representatives].sort((a, b) =>
      `${b.date} ${b.time}`.localeCompare(`${a.date} ${a.time}`)
    );
  }

  function review(id: number) {
    navigation.navigate(`/ui/detections/${id}?tab=review`);
  }
</script>

{#if loading}
  <div class="flex items-center gap-2 text-sm opacity-70 py-2">
    <span class="loading loading-spinner loading-sm"></span>
    {t('soundnet.events.reviewLoading')}
  </div>
{:else if error}
  <div role="alert" class="alert alert-error text-sm">
    <TriangleAlert class="h-4 w-4" aria-hidden="true" />
    <span>{t('soundnet.events.reviewLoadFailed', { error })}</span>
  </div>
{:else if rows.length === 0}
  <p class="flex items-center gap-2 text-sm opacity-70">
    <CircleCheck class="h-4 w-4" aria-hidden="true" />
    {t('soundnet.events.reviewEmpty')}
  </p>
{:else}
  <p class="text-xs opacity-60 mb-2">
    {t('soundnet.events.reviewCount', { count: rows.length })}
  </p>
  <ul class="divide-y divide-base-200">
    {#each rows as d (d.id)}
      <li>
        <button
          type="button"
          class="w-full flex items-center gap-3 py-2 px-1 text-left hover:bg-base-200 rounded"
          onclick={() => review(d.id)}
        >
          <span class="font-mono text-xs opacity-60 w-24 shrink-0">{d.date} {d.time}</span>
          <span class="font-medium truncate">{d.eventDisplayName}</span>
          <span class="font-mono text-xs opacity-60">{d.confidence.toFixed(2)}</span>
          {#if d.resolvedDomain}
            <span class="badge badge-warning badge-xs">
              {t('detections.resolvedDomain', { domain: d.resolvedDomain })}
            </span>
          {/if}
          <ChevronRight class="h-4 w-4 ml-auto opacity-50" aria-hidden="true" />
        </button>
      </li>
    {/each}
  </ul>
{/if}
