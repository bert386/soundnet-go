<!--
  CategoryFilter.svelte - narrow the detection list to a kind of event

  Purpose: make a list dominated by birds usable when what you came for is an
  aircraft, a siren or a chainsaw.

  The options are fetched rather than hardcoded. They come from the event
  taxonomy via /api/v2/detections/categories, so adding a domain in
  internal/eventclass makes it appear here with no frontend change - a list
  duplicated in two places is a list that drifts.

  "All" is the absence of a filter, not a filter meaning everything: it removes
  the query parameter entirely, so the URL of an unfiltered list stays clean and
  shareable.
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import { cn } from '$lib/utils/cn';
  import { Filter } from '@lucide/svelte';

  const logger = loggers.ui;

  interface CategoryOption {
    id: string;
    labels: number;
  }

  interface Props {
    /** The currently applied category, or undefined when unfiltered. */
    selected?: string | undefined;
    onChange: (_category: string | undefined) => void;
    className?: string;
  }

  let { selected = undefined, onChange, className = '' }: Props = $props();

  let options = $state<CategoryOption[]>([]);
  let failed = $state(false);

  $effect(() => {
    fetch('/api/v2/detections/categories')
      .then(async response => {
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
        return (await response.json()) as { categories: CategoryOption[] };
      })
      .then(body => {
        // A category matching no labels can never return a detection, so
        // offering it would only produce an empty list the user cannot explain.
        options = (body.categories ?? []).filter(c => c.labels > 0);
      })
      .catch((err: unknown) => {
        // A missing filter is a degraded list, not a broken page: the
        // unfiltered detections are still perfectly usable, so say nothing
        // louder than a hidden control.
        logger.error('failed to load detection categories', err);
        failed = true;
      });
  });

  // Translate when we have a string for it, otherwise show the domain id. A new
  // domain should appear in the UI immediately rather than wait for a
  // translation, even if it appears untranslated.
  function label(id: string): string {
    const key = `soundnet.categories.${id}`;
    const translated = t(key as never);
    return translated === key ? id : translated;
  }

  function select(id: string | undefined): void {
    onChange(id === selected ? undefined : id);
  }
</script>

{#if !failed && options.length > 0}
  <div class={cn('flex flex-wrap items-center gap-2', className)}>
    <span class="flex items-center gap-1 text-sm opacity-70">
      <Filter class="h-3.5 w-3.5" aria-hidden="true" />
      {t('soundnet.categories.label')}
    </span>

    <div class="join" role="group" aria-label={t('soundnet.categories.label')}>
      <button
        type="button"
        class={cn('btn btn-xs join-item', selected === undefined && 'btn-active btn-primary')}
        aria-pressed={selected === undefined}
        onclick={() => select(undefined)}
      >
        {t('soundnet.categories.all')}
      </button>
      {#each options as option (option.id)}
        <button
          type="button"
          class={cn('btn btn-xs join-item', selected === option.id && 'btn-active btn-primary')}
          aria-pressed={selected === option.id}
          onclick={() => select(option.id)}
        >
          {label(option.id)}
        </button>
      {/each}
    </div>
  </div>
{/if}
