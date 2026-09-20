<!--
  EventsPage.svelte - the non-bird side of the station, in one place

  Why this page exists: the review tooling and the training export were both
  built and neither was reachable.

  Review hands off to the detection page BirdNET-Go already has, rather than
  reviewing anything here. That page shows the spectrogram, which is most of what
  tells a jet from a thunder clap by eye, and it is the review the operator
  already uses. An earlier version of this page carried its own keyboard-driven
  queue; the operator preferred theirs, and they were right. No route pointed at them, so the only way to record
  that a detection was wrong was to type a sentence into its comment box - which
  is what the operator has been doing, for dozens of detections, producing prose
  where the training export needs structure.

  A correction and a comment are not the same thing. "Wrong" and "wrong, and it
  was actually a passing jet" differ by exactly the information a retrain needs,
  and only the second makes a confusion pair.

  The page is deliberately events-first rather than species-first. The rest of
  the UI is built around a bird list, which is right for what it was written
  for; this is for everything else the station hears.
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import { navigation } from '$lib/stores/navigation.svelte';
  import { Activity, ListChecks, Download, Filter } from '@lucide/svelte';

  import EventReviewList from '../components/EventReviewList.svelte';
  import TrainingExport from '../components/TrainingExport.svelte';
  import CategoryFilter from '../components/CategoryFilter.svelte';

  let category = $state<string | undefined>(undefined);

  // The detections list is where the rows themselves live, and it already
  // filters by category. Sending the operator there with the filter applied
  // beats reimplementing a second list that would drift from the first.
  function browseCategory(next: string | undefined) {
    category = next;
    const query = next ? `?category=${encodeURIComponent(next)}` : '';
    navigation.navigate(`/ui/detections${query}`);
  }
</script>

<div class="col-span-12 space-y-4">
  <div class="card bg-base-100 shadow-sm">
    <div class="card-body p-4 sm:p-6">
      <h2 class="card-title text-lg gap-2">
        <Activity class="h-5 w-5" aria-hidden="true" />
        {t('soundnet.events.title')}
      </h2>
      <p class="text-sm opacity-70">{t('soundnet.events.intro')}</p>

      <div class="mt-3 flex flex-wrap items-center gap-2">
        <Filter class="h-4 w-4 opacity-60" aria-hidden="true" />
        <span class="text-sm opacity-70">{t('soundnet.events.browse')}</span>
        <CategoryFilter selected={category} onChange={browseCategory} />
      </div>
    </div>
  </div>

  <div class="card bg-base-100 shadow-sm">
    <div class="card-body p-4 sm:p-6">
      <h3 class="card-title text-base gap-2">
        <ListChecks class="h-4 w-4" aria-hidden="true" />
        {t('soundnet.events.reviewHeading')}
      </h3>
      <p class="text-sm opacity-70">{t('soundnet.events.reviewIntro')}</p>
      <div class="mt-3">
        <EventReviewList />
      </div>
    </div>
  </div>

  <div class="card bg-base-100 shadow-sm">
    <div class="card-body p-4 sm:p-6">
      <h3 class="card-title text-base gap-2">
        <Download class="h-4 w-4" aria-hidden="true" />
        {t('soundnet.events.exportHeading')}
      </h3>
      <p class="text-sm opacity-70">{t('soundnet.events.exportIntro')}</p>
      <div class="mt-3">
        <TrainingExport />
      </div>
    </div>
  </div>
</div>
