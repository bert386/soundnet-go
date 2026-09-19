<!--
  ReviewQueue.svelte - rapid verification of unreviewed detections

  Purpose: turn a backlog of unreviewed detections into labelled training data
  with as little friction as possible.

  The design constraint is throughput. An operator reviewing a night's detections
  is doing the same three-way decision hundreds of times, so every interaction is
  reachable from the keyboard and the queue advances on its own. Reaching for a
  mouse between each one is what makes review backlogs never get cleared.

  Correcting is deliberately a distinct action from rejecting. "Wrong" and
  "wrong, and here is the right answer" are different amounts of information, and
  only the second produces a confusion pair worth retraining on.
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import { Check, X, PenLine, SkipForward, Keyboard, TriangleAlert } from '@lucide/svelte';

  const logger = loggers.ui;

  interface QueueDetection {
    id: number;
    commonName: string;
    scientificName: string;
    confidence: number;
    date: string;
    time: string;
    clipName?: string;
  }

  let queue = $state<QueueDetection[]>([]);
  let index = $state(0);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let busy = $state(false);
  let correcting = $state(false);
  let correctionLabel = $state('');
  let reviewedCount = $state(0);

  const current = $derived(queue.at(index) ?? null);
  const remaining = $derived(Math.max(queue.length - index, 0));

  async function loadQueue(): Promise<void> {
    loading = true;
    error = null;
    try {
      // Only unverified detections: the queue exists to shrink, so showing
      // already-reviewed ones would make progress invisible.
      const response = await fetch('/api/v2/detections?queryType=search&verified=unverified&numResults=100');
      if (!response.ok) throw new Error(`HTTP ${response.status}`);
      const body = (await response.json()) as { data?: QueueDetection[] };
      queue = body.data ?? [];
      index = 0;
    } catch (err: unknown) {
      logger.error('failed to load review queue', err);
      error = err instanceof Error ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  $effect(() => {
    void loadQueue();
  });

  function advance(): void {
    correcting = false;
    correctionLabel = '';
    index += 1;
  }

  async function review(verified: 'correct' | 'false_positive'): Promise<void> {
    const detection = current;
    if (!detection || busy) return;
    busy = true;
    try {
      const response = await fetch(`/api/v2/detections/${detection.id}/review`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ verified }),
      });
      if (!response.ok) throw new Error(`HTTP ${response.status}`);
      reviewedCount += 1;
      advance();
    } catch (err: unknown) {
      // The queue does not advance on failure. Silently skipping would lose the
      // operator's decision without telling them.
      logger.error('review failed', err);
      error = err instanceof Error ? err.message : String(err);
    } finally {
      busy = false;
    }
  }

  async function submitCorrection(): Promise<void> {
    const detection = current;
    const labelId = Number.parseInt(correctionLabel, 10);
    if (!detection || busy || Number.isNaN(labelId) || labelId <= 0) return;
    busy = true;
    try {
      const response = await fetch(`/api/v2/soundnet/detections/${detection.id}/correction`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ correctedLabelId: labelId }),
      });
      if (!response.ok) throw new Error(`HTTP ${response.status}`);
      reviewedCount += 1;
      advance();
    } catch (err: unknown) {
      logger.error('correction failed', err);
      error = err instanceof Error ? err.message : String(err);
    } finally {
      busy = false;
    }
  }

  function handleKey(event: KeyboardEvent): void {
    // While typing a correction the keys mean characters, not commands.
    if (correcting) {
      if (event.key === 'Escape') {
        correcting = false;
        correctionLabel = '';
      }
      return;
    }
    switch (event.key.toLowerCase()) {
      case 'c':
        void review('correct');
        break;
      case 'f':
        void review('false_positive');
        break;
      case 'e':
        event.preventDefault();
        correcting = true;
        break;
      case 's':
        advance();
        break;
      default:
        break;
    }
  }
</script>

<svelte:window on:keydown={handleKey} />

<div class="card bg-base-100 shadow-sm">
  <div class="card-body p-4 sm:p-6">
    <div class="flex items-start justify-between gap-4">
      <div>
        <h2 class="card-title text-lg">{t('soundnet.review.title')}</h2>
        <p class="text-sm opacity-70">
          {t('soundnet.review.progress', { remaining, reviewed: reviewedCount })}
        </p>
      </div>
      <div class="hidden sm:flex items-center gap-1 text-xs opacity-60">
        <Keyboard class="h-3.5 w-3.5" aria-hidden="true" />
        {t('soundnet.review.shortcuts')}
      </div>
    </div>

    {#if error}
      <div role="alert" class="alert alert-error text-sm mt-3">
        <TriangleAlert class="h-4 w-4" aria-hidden="true" />
        <span>{t('soundnet.review.failed', { error })}</span>
        <button class="btn btn-sm" onclick={() => { error = null; }}>
          {t('common.dismiss')}
        </button>
      </div>
    {/if}

    {#if loading}
      <div class="flex items-center gap-2 text-sm opacity-70 py-6">
        <span class="loading loading-spinner loading-sm"></span>
        {t('soundnet.review.loading')}
      </div>
    {:else if !current}
      <!-- An empty queue is a result, not a blank screen. -->
      <div class="py-8 text-center">
        <p class="font-medium">{t('soundnet.review.empty')}</p>
        <p class="text-sm opacity-70 mt-1">{t('soundnet.review.emptyHint')}</p>
        <button class="btn btn-sm btn-outline mt-4" onclick={() => void loadQueue()}>
          {t('soundnet.review.reload')}
        </button>
      </div>
    {:else}
      <article class="mt-4">
        <h3 class="text-xl font-semibold">{current.commonName}</h3>
        <p class="text-sm opacity-70">
          {current.scientificName} · {current.date} {current.time} ·
          {t('soundnet.review.confidence', { percent: Math.round(current.confidence * 100) })}
        </p>

        {#if current.clipName}
          <!-- Hearing the clip is the whole basis of the judgement. -->
          <audio
            class="w-full mt-3"
            controls
            preload="none"
            src={`/api/v2/audio/${current.id}`}
          ></audio>
        {/if}

        {#if correcting}
          <div class="mt-4">
            <label class="label" for="correction-label">
              <span class="label-text">{t('soundnet.review.correctionLabel')}</span>
            </label>
            <div class="flex gap-2">
              <input
                id="correction-label"
                class="input input-bordered flex-1"
                type="number"
                min="1"
                bind:value={correctionLabel}
                placeholder={t('soundnet.review.correctionPlaceholder')}
              />
              <button
                class="btn btn-primary"
                disabled={busy || !correctionLabel}
                onclick={() => void submitCorrection()}
              >
                {t('soundnet.review.saveCorrection')}
              </button>
            </div>
            <p class="text-xs opacity-60 mt-2">{t('soundnet.review.correctionHint')}</p>
          </div>
        {:else}
          <div class="flex flex-wrap gap-2 mt-4">
            <button class="btn btn-success btn-sm" disabled={busy} onclick={() => void review('correct')}>
              <Check class="h-4 w-4" aria-hidden="true" />
              {t('soundnet.review.confirm')} <kbd class="kbd kbd-xs ml-1">C</kbd>
            </button>
            <button class="btn btn-error btn-sm" disabled={busy} onclick={() => void review('false_positive')}>
              <X class="h-4 w-4" aria-hidden="true" />
              {t('soundnet.review.falsePositive')} <kbd class="kbd kbd-xs ml-1">F</kbd>
            </button>
            <button class="btn btn-warning btn-sm" disabled={busy} onclick={() => { correcting = true; }}>
              <PenLine class="h-4 w-4" aria-hidden="true" />
              {t('soundnet.review.correct')} <kbd class="kbd kbd-xs ml-1">E</kbd>
            </button>
            <button class="btn btn-ghost btn-sm" disabled={busy} onclick={advance}>
              <SkipForward class="h-4 w-4" aria-hidden="true" />
              {t('soundnet.review.skip')} <kbd class="kbd kbd-xs ml-1">S</kbd>
            </button>
          </div>
          <!--
            Stated because the distinction is the point: rejecting records that
            the model was wrong, correcting records what was right. Only the
            second produces a confusion pair worth retraining on.
          -->
          <p class="text-xs opacity-60 mt-3">{t('soundnet.review.correctVsReject')}</p>
        {/if}
      </article>
    {/if}
  </div>
</div>
