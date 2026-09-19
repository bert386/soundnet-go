<!--
  SoundNetPanel.svelte - SoundNet diagnostics and identity for one detection

  Purpose: show the two layers SoundNet adds on top of classification - the
  measured properties of the sound, and the externally resolved identity of what
  made it.

  The panel is as careful about absence as about data. "No diagnostics" can mean
  the domain has nothing measurable, or that the layer is switched off, or that
  it ran and found nothing worth reporting - and those are entirely different
  things to tell an operator. The API returns which case applies and this renders
  it, rather than showing an empty box.

  Props:
  - detectionId: string - the detection to describe
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import {
    Activity,
    Gauge,
    Plane,
    Ruler,
    Timer,
    TriangleAlert,
    Waves,
  } from '@lucide/svelte';

  const logger = loggers.ui;

  interface Props {
    detectionId: string;
  }

  let { detectionId }: Props = $props();

  interface LevelResult {
    peak_dbfs: number;
    rms_dbfs: number;
    spectral_tilt_db_per_decade: number;
    proximity: string;
    absolute_distance_m?: number;
    band_count: number;
  }

  interface OnsetResult {
    count: number;
    times_sec: number[];
    mean_interval_sec?: number;
    stddev_interval_sec?: number;
    regular: boolean;
  }

  interface EnvelopeResult {
    attack_sec: number;
    decay_sec: number;
    duration_sec: number;
  }

  interface DopplerResult {
    speed_kmh: number;
    cpa_metres: number;
    cpa_time_sec: number;
    rest_frequency_hz: number;
    confidence: number;
  }

  interface ImpulseResult {
    crest_factor: number;
    rise_time_ms: number;
    spectral_centroid_hz: number;
    low_confidence: boolean;
    ambiguity?: string;
  }

  interface DiagnosticsPayload {
    schema_version: number;
    level?: LevelResult;
    onsets?: OnsetResult;
    envelope?: EnvelopeResult;
    doppler?: DopplerResult;
    impulse?: ImpulseResult;
    warnings?: string[];
  }

  interface Diagnostics {
    schemaVersion: number;
    computeMs: number;
    payload: DiagnosticsPayload;
    computedAt: string;
  }

  interface Enrichment {
    provider: string;
    source: string;
    confidence: number;
    lagCorrectionMs: number;
    attributes: Record<string, string | number | boolean>;
    resolvedAt: string;
  }

  interface SoundNetResponse {
    detectionId: number;
    domain: string;
    diagnosable: boolean;
    enrichable: boolean;
    diagnostics?: Diagnostics;
    enrichment?: Enrichment[];
    reviewState: string;
    note?: string;
  }

  let data = $state<SoundNetResponse | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);

  $effect(() => {
    const id = detectionId;
    if (!id) return;

    loading = true;
    error = null;

    fetch(`/api/v2/soundnet/detections/${encodeURIComponent(id)}`)
      .then(async response => {
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
        return (await response.json()) as SoundNetResponse;
      })
      .then(result => {
        data = result;
        loading = false;
      })
      .catch((err: unknown) => {
        // Surfaced rather than swallowed: a panel that silently shows nothing
        // when the request failed is indistinguishable from one correctly
        // reporting that there is nothing to show.
        logger.error('failed to load SoundNet data', err);
        error = err instanceof Error ? err.message : String(err);
        loading = false;
      });
  });

  const diagnostics = $derived(data?.diagnostics?.payload);

  // Proximity is a comparative judgement, not a distance. The label says so.
  const proximityLabel = $derived.by(() => {
    switch (diagnostics?.level?.proximity) {
      case 'near':
        return t('soundnet.proximity.near');
      case 'mid':
        return t('soundnet.proximity.mid');
      case 'far':
        return t('soundnet.proximity.far');
      default:
        return t('soundnet.proximity.unknown');
    }
  });

  const aircraft = $derived(data?.enrichment?.find(e => e.provider === 'adsb'));

  // Attributes arrive from a provider, so they are untrusted keys on an untrusted
  // object. Reading them through a Map avoids indexing a plain object with a
  // dynamic key, which would let a crafted payload reach Object.prototype.
  const aircraftAttributes = $derived(
    new Map<string, string>(
      Object.entries(aircraft?.attributes ?? {}).map(([key, value]) => [key, String(value)])
    )
  );

  function attr(key: string): string | null {
    const value = aircraftAttributes.get(key);
    return value === undefined || value === '' ? null : value;
  }

  function round(value: number, places = 1): string {
    return value.toFixed(places);
  }
</script>

<div class="card bg-base-100 shadow-sm">
  <div class="card-body p-4 sm:p-6">
    <h3 class="card-title text-base gap-2">
      <Activity class="h-4 w-4" aria-hidden="true" />
      {t('soundnet.title')}
    </h3>

    {#if loading}
      <div class="flex items-center gap-2 text-sm opacity-70 py-4">
        <span class="loading loading-spinner loading-sm"></span>
        {t('soundnet.loading')}
      </div>
    {:else if error}
      <div role="alert" class="alert alert-error text-sm">
        <TriangleAlert class="h-4 w-4" aria-hidden="true" />
        <span>{t('soundnet.loadFailed', { error })}</span>
      </div>
    {:else if data}
      <div class="flex flex-wrap items-center gap-2 text-xs">
        <span class="badge badge-ghost">{t('soundnet.domain', { domain: data.domain })}</span>
        {#if data.diagnostics}
          <span class="badge badge-ghost" title={t('soundnet.computeTimeHint')}>
            <Timer class="h-3 w-3 mr-1" aria-hidden="true" />
            {data.diagnostics.computeMs} ms
          </span>
        {/if}
      </div>

      <!-- Absence is explained, never left as an empty panel. -->
      {#if data.note}
        <p class="text-sm opacity-70 mt-2">{data.note}</p>
      {/if}

      {#if diagnostics?.level}
        <section class="mt-4">
          <h4 class="font-medium text-sm flex items-center gap-1.5">
            <Ruler class="h-3.5 w-3.5" aria-hidden="true" />
            {t('soundnet.level.heading')}
          </h4>
          <dl class="grid grid-cols-2 sm:grid-cols-4 gap-3 mt-2 text-sm">
            <div>
              <dt class="opacity-60 text-xs">{t('soundnet.level.proximity')}</dt>
              <dd class="font-medium">{proximityLabel}</dd>
            </div>
            <div>
              <dt class="opacity-60 text-xs">{t('soundnet.level.tilt')}</dt>
              <dd class="font-mono">{round(diagnostics.level.spectral_tilt_db_per_decade)} dB/dec</dd>
            </div>
            <div>
              <dt class="opacity-60 text-xs">{t('soundnet.level.peak')}</dt>
              <dd class="font-mono">{round(diagnostics.level.peak_dbfs)} dBFS</dd>
            </div>
            <div>
              <dt class="opacity-60 text-xs">{t('soundnet.level.rms')}</dt>
              <dd class="font-mono">{round(diagnostics.level.rms_dbfs)} dBFS</dd>
            </div>
          </dl>
          <!-- Stated plainly: without a calibrated microphone this is relative. -->
          <p class="text-xs opacity-60 mt-2">
            {#if diagnostics.level.absolute_distance_m}
              {t('soundnet.level.calibrated', {
                metres: round(diagnostics.level.absolute_distance_m, 0),
              })}
            {:else}
              {t('soundnet.level.relativeOnly')}
            {/if}
          </p>
        </section>
      {/if}

      {#if diagnostics?.onsets && diagnostics.onsets.count > 0}
        <section class="mt-4">
          <h4 class="font-medium text-sm flex items-center gap-1.5">
            <Waves class="h-3.5 w-3.5" aria-hidden="true" />
            {t('soundnet.onsets.heading')}
          </h4>
          <p class="text-2xl font-semibold mt-1">{diagnostics.onsets.count}</p>
          <p class="text-xs opacity-70">
            {#if diagnostics.onsets.mean_interval_sec}
              {t('soundnet.onsets.interval', {
                seconds: round(diagnostics.onsets.mean_interval_sec, 2),
              })}
              {#if diagnostics.onsets.regular}
                · {t('soundnet.onsets.regular')}
              {/if}
            {:else}
              {t('soundnet.onsets.single')}
            {/if}
          </p>
        </section>
      {/if}

      {#if diagnostics?.doppler}
        <section class="mt-4">
          <h4 class="font-medium text-sm flex items-center gap-1.5">
            <Gauge class="h-3.5 w-3.5" aria-hidden="true" />
            {t('soundnet.doppler.heading')}
          </h4>
          <dl class="grid grid-cols-2 sm:grid-cols-3 gap-3 mt-2 text-sm">
            <div>
              <dt class="opacity-60 text-xs">{t('soundnet.doppler.speed')}</dt>
              <dd class="font-medium">{round(diagnostics.doppler.speed_kmh, 0)} km/h</dd>
            </div>
            <div>
              <dt class="opacity-60 text-xs">{t('soundnet.doppler.cpa')}</dt>
              <dd class="font-medium">{round(diagnostics.doppler.cpa_metres, 0)} m</dd>
            </div>
            <div>
              <dt class="opacity-60 text-xs">{t('soundnet.doppler.fit')}</dt>
              <dd class="font-mono">{round(diagnostics.doppler.confidence * 100, 0)}%</dd>
            </div>
          </dl>
          {#if diagnostics.doppler.confidence < 0.5}
            <p class="text-xs opacity-70 mt-2">{t('soundnet.doppler.weakFit')}</p>
          {/if}
        </section>
      {/if}

      {#if diagnostics?.envelope}
        <section class="mt-4">
          <h4 class="font-medium text-sm">{t('soundnet.envelope.heading')}</h4>
          <p class="text-sm mt-1">
            {t('soundnet.envelope.duration', {
              seconds: round(diagnostics.envelope.duration_sec, 2),
            })}
          </p>
        </section>
      {/if}

      {#if diagnostics?.impulse}
        <section class="mt-4">
          <h4 class="font-medium text-sm">{t('soundnet.impulse.heading')}</h4>
          <dl class="grid grid-cols-3 gap-3 mt-2 text-sm">
            <div>
              <dt class="opacity-60 text-xs">{t('soundnet.impulse.crest')}</dt>
              <dd class="font-mono">{round(diagnostics.impulse.crest_factor)}</dd>
            </div>
            <div>
              <dt class="opacity-60 text-xs">{t('soundnet.impulse.rise')}</dt>
              <dd class="font-mono">{round(diagnostics.impulse.rise_time_ms, 2)} ms</dd>
            </div>
            <div>
              <dt class="opacity-60 text-xs">{t('soundnet.impulse.centroid')}</dt>
              <dd class="font-mono">{round(diagnostics.impulse.spectral_centroid_hz, 0)} Hz</dd>
            </div>
          </dl>
          <!--
            The scope is explicit that gunshot versus backfire cannot be settled
            from one microphone. The features inform a human judgement; they do
            not make the call, and the UI must not imply that they did.
          -->
          {#if diagnostics.impulse.low_confidence}
            <div role="note" class="alert alert-warning text-xs mt-2 py-2">
              <TriangleAlert class="h-3.5 w-3.5" aria-hidden="true" />
              <span>{diagnostics.impulse.ambiguity ?? t('soundnet.impulse.lowConfidence')}</span>
            </div>
          {/if}
        </section>
      {/if}

      {#if aircraft}
        <section class="mt-4 border-t pt-3">
          <h4 class="font-medium text-sm flex items-center gap-1.5">
            <Plane class="h-3.5 w-3.5" aria-hidden="true" />
            {t('soundnet.aircraft.heading')}
          </h4>
          <p class="text-lg font-semibold mt-1">
            {attr('flight_iata') ?? attr('callsign') ?? attr('hex')}
          </p>
          <dl class="grid grid-cols-2 sm:grid-cols-3 gap-3 mt-2 text-sm">
            {#if attr('registration')}
              <div>
                <dt class="opacity-60 text-xs">{t('soundnet.aircraft.registration')}</dt>
                <dd class="font-mono">{attr('registration')}</dd>
              </div>
            {/if}
            {#if attr('type_code')}
              <div>
                <dt class="opacity-60 text-xs">{t('soundnet.aircraft.type')}</dt>
                <dd>{attr('type_name') ?? attr('type_code')}</dd>
              </div>
            {/if}
            {#if attr('operator')}
              <div>
                <dt class="opacity-60 text-xs">{t('soundnet.aircraft.operator')}</dt>
                <dd>{attr('operator')}</dd>
              </div>
            {/if}
            {#if attr('origin_iata') && attr('destination_iata')}
              <div class="col-span-2">
                <dt class="opacity-60 text-xs">{t('soundnet.aircraft.route')}</dt>
                <dd>
                  {attr('origin_iata')} → {attr('destination_iata')}
                </dd>
              </div>
            {/if}
            {#if attr('altitude_m')}
              <div>
                <dt class="opacity-60 text-xs">{t('soundnet.aircraft.altitude')}</dt>
                <dd class="font-mono">{attr('altitude_m')} m</dd>
              </div>
            {/if}
          </dl>
          <!--
            The lag is what ties this audio to that aircraft. Showing it lets an
            operator sanity-check a surprising match instead of taking it on faith.
          -->
          <p class="text-xs opacity-60 mt-2">
            {t('soundnet.aircraft.provenance', {
              lag: round(aircraft.lagCorrectionMs / 1000, 1),
              confidence: round(aircraft.confidence * 100, 0),
              source: aircraft.source,
            })}
          </p>
        </section>
      {:else if data.enrichable && data.diagnostics}
        <p class="text-xs opacity-60 mt-3">{t('soundnet.aircraft.noMatch')}</p>
      {/if}

      {#if diagnostics?.warnings?.length}
        <ul class="text-xs opacity-60 mt-3 list-disc list-inside">
          {#each diagnostics.warnings as warning (warning)}
            <li>{warning}</li>
          {/each}
        </ul>
      {/if}
    {/if}
  </div>
</div>
