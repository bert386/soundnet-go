<!--
  AircraftCard.svelte - one aircraft the station identified

  The species pages give every bird a photograph. The aircraft that fly over
  this station get a grey silhouette in a list called "45 species", which is
  most of why the reporting reads as bird software with other things bolted on.
  An aeroplane has a photograph too, and a track anyone can look at.

  The photograph and the outside links come from lib/soundnet/aircraftPhoto,
  which holds the terms this station has to keep to and caches a hex code across
  every card on the page. See that module for why it is fetched in the browser
  rather than proxied.
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import { Plane, ExternalLink, Ruler, Clock } from '@lucide/svelte';
  import {
    aircraftPhoto,
    flightPathUrl,
    aircraftRecordUrl,
    type AircraftPhoto,
  } from '../aircraftPhoto';

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

  interface Props {
    aircraft: Sighting;
  }

  let { aircraft }: Props = $props();

  let photo = $state<AircraftPhoto | null>(null);

  $effect(() => {
    const hex = aircraft.hex;
    if (!hex) return;
    photo = null;

    let current = true;
    aircraftPhoto(hex).then(found => {
      // A card that has moved on to another aircraft must not be overwritten by
      // a reply about the previous one.
      if (current) photo = found;
    });
    return () => {
      current = false;
    };
  });

  const title = $derived(aircraft.registration || aircraft.callsign || aircraft.hex);
  const subtitle = $derived(
    [aircraft.typeName || aircraft.typeCode, aircraft.operator].filter(Boolean).join(' · ')
  );

  const trackUrl = $derived(flightPathUrl(aircraft.hex));
  const registryUrl = $derived(aircraftRecordUrl(aircraft.registration));

  function when(iso: string): string {
    const d = new Date(iso);
    return Number.isNaN(d.getTime()) ? '' : d.toLocaleString();
  }
</script>

<div class="card bg-base-100 shadow-sm overflow-hidden">
  {#if photo}
    <figure class="h-32 bg-base-200">
      <img src={photo.src} alt={title} class="w-full h-32 object-cover" loading="lazy" />
    </figure>
  {:else}
    <figure class="h-32 bg-base-200 flex items-center justify-center">
      <Plane class="h-10 w-10 opacity-20" aria-hidden="true" />
    </figure>
  {/if}

  <div class="card-body p-3 gap-1">
    <div class="flex items-baseline gap-2">
      <span class="font-semibold">{title}</span>
      {#if aircraft.typeCode}
        <span class="badge badge-ghost badge-sm font-mono">{aircraft.typeCode}</span>
      {/if}
    </div>
    {#if subtitle}
      <p class="text-xs opacity-70 truncate">{subtitle}</p>
    {/if}

    <dl class="flex flex-wrap gap-x-4 gap-y-1 text-xs mt-1">
      <div class="flex items-center gap-1">
        <Plane class="h-3 w-3 opacity-60" aria-hidden="true" />
        <dd>{t('soundnet.aircraftCard.detections', { count: aircraft.detections })}</dd>
      </div>
      {#if aircraft.closestKm}
        <div class="flex items-center gap-1">
          <Ruler class="h-3 w-3 opacity-60" aria-hidden="true" />
          <dd class="font-mono">{aircraft.closestKm.toFixed(1)} km</dd>
        </div>
      {/if}
      <div class="flex items-center gap-1">
        <Clock class="h-3 w-3 opacity-60" aria-hidden="true" />
        <dd>{when(aircraft.lastSeen)}</dd>
      </div>
    </dl>

    <div class="flex flex-wrap items-center gap-2 mt-2">
      <a
        href={trackUrl}
        target="_blank"
        rel="noopener noreferrer"
        class="btn btn-xs btn-outline gap-1"
      >
        {t('soundnet.aircraftCard.track')}
        <ExternalLink class="h-3 w-3" aria-hidden="true" />
      </a>
      {#if registryUrl}
        <a
          href={registryUrl}
          target="_blank"
          rel="noopener noreferrer"
          class="btn btn-xs btn-ghost gap-1"
        >
          {t('soundnet.aircraftCard.registry')}
          <ExternalLink class="h-3 w-3" aria-hidden="true" />
        </a>
      {/if}
    </div>

    {#if photo && photo.photographer}
      <!-- Attribution is a condition of using the photograph, not a courtesy. -->
      <p class="text-[10px] opacity-50 mt-1">
        <a href={photo.link} target="_blank" rel="noopener noreferrer" class="hover:underline">
          {t('soundnet.aircraftCard.photoCredit', { photographer: photo.photographer })}
        </a>
      </p>
    {/if}
  </div>
</div>
