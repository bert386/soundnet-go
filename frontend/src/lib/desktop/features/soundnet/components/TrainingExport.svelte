<!--
  TrainingExport.svelte - what a retrain would actually learn from

  Purpose: show the labelled corpus built up by review, so an operator can see
  whether retraining is worth attempting yet.

  The useful part is not the total. It is which classes are too thin to train on:
  a class with three examples will not produce a usable head, and finding that
  out after a training run wastes an afternoon. So thin classes are called out
  rather than folded into a headline figure.
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import { Database, TriangleAlert, PenLine, Check } from '@lucide/svelte';

  const logger = loggers.ui;

  interface ExportSummary {
    total: number;
    perLabel: Record<string, number>;
    thinClasses: string[];
    minUsable: number;
    examples: Array<{ detectionId: number; label: string; origin: string }>;
  }

  let summary = $state<ExportSummary | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);

  $effect(() => {
    loading = true;
    fetch('/api/v2/soundnet/training-export')
      .then(async response => {
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
        return (await response.json()) as ExportSummary;
      })
      .then(result => {
        summary = result;
        loading = false;
      })
      .catch((err: unknown) => {
        logger.error('failed to load training export', err);
        error = err instanceof Error ? err.message : String(err);
        loading = false;
      });
  });

  // Corrections are the valuable half: each marks a case the model got wrong,
  // which is where the training signal actually is.
  const correctedCount = $derived(
    summary?.examples.filter(e => e.origin === 'corrected').length ?? 0
  );
  const confirmedCount = $derived(
    summary?.examples.filter(e => e.origin === 'confirmed').length ?? 0
  );

  const labelRows = $derived(Object.entries(summary?.perLabel ?? {}).sort((a, b) => b[1] - a[1]));

  function downloadManifest(): void {
    // The manifest references clips rather than embedding them, so this is a
    // small file describing where the audio already lives.
    window.open('/api/v2/soundnet/training-export', '_blank');
  }
</script>

<div class="card bg-base-100 shadow-sm">
  <div class="card-body p-4 sm:p-6">
    <h2 class="card-title text-lg gap-2">
      <Database class="h-4 w-4" aria-hidden="true" />
      {t('soundnet.export.title')}
    </h2>

    {#if loading}
      <div class="flex items-center gap-2 text-sm opacity-70 py-4">
        <span class="loading loading-spinner loading-sm"></span>
        {t('soundnet.export.loading')}
      </div>
    {:else if error}
      <div role="alert" class="alert alert-error text-sm">
        <TriangleAlert class="h-4 w-4" aria-hidden="true" />
        <span>{t('soundnet.export.failed', { error })}</span>
      </div>
    {:else if summary}
      <div class="stats stats-vertical sm:stats-horizontal shadow-none mt-2">
        <div class="stat px-0 sm:px-4">
          <div class="stat-title text-xs">{t('soundnet.export.total')}</div>
          <div class="stat-value text-2xl">{summary.total}</div>
        </div>
        <div class="stat px-0 sm:px-4">
          <div class="stat-title text-xs flex items-center gap-1">
            <PenLine class="h-3 w-3" aria-hidden="true" />
            {t('soundnet.export.corrected')}
          </div>
          <div class="stat-value text-2xl">{correctedCount}</div>
          <div class="stat-desc text-xs">{t('soundnet.export.correctedHint')}</div>
        </div>
        <div class="stat px-0 sm:px-4">
          <div class="stat-title text-xs flex items-center gap-1">
            <Check class="h-3 w-3" aria-hidden="true" />
            {t('soundnet.export.confirmed')}
          </div>
          <div class="stat-value text-2xl">{confirmedCount}</div>
        </div>
      </div>

      {#if summary.thinClasses.length > 0}
        <!--
          The point of this panel. A class with a handful of examples will not
          produce a usable head, and discovering that after a training run
          wastes an afternoon.
        -->
        <div role="note" class="alert alert-warning text-sm mt-4">
          <TriangleAlert class="h-4 w-4" aria-hidden="true" />
          <div>
            <p class="font-medium">
              {t('soundnet.export.thinHeading', { count: summary.minUsable })}
            </p>
            <p class="text-xs mt-1">{summary.thinClasses.join(' · ')}</p>
          </div>
        </div>
      {/if}

      {#if labelRows.length > 0}
        <div class="overflow-x-auto mt-4">
          <table class="table table-sm">
            <thead>
              <tr>
                <th>{t('soundnet.export.label')}</th>
                <th class="text-right">{t('soundnet.export.examples')}</th>
              </tr>
            </thead>
            <tbody>
              {#each labelRows as [label, count] (label)}
                <tr>
                  <td>{label}</td>
                  <td class="text-right font-mono" class:opacity-50={count < summary.minUsable}>
                    {count}
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>

        <div class="card-actions justify-end mt-4">
          <button class="btn btn-sm btn-outline" onclick={downloadManifest}>
            {t('soundnet.export.download')}
          </button>
        </div>
        <p class="text-xs opacity-60 mt-2">{t('soundnet.export.manifestNote')}</p>
      {:else}
        <div class="py-6 text-center">
          <p class="font-medium">{t('soundnet.export.empty')}</p>
          <p class="text-sm opacity-70 mt-1">{t('soundnet.export.emptyHint')}</p>
        </div>
      {/if}
    {/if}
  </div>
</div>
