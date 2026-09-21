<!--
  EventThumbnail.svelte - the picture beside a non-bird detection

  Every detection row in this UI carries a bird photograph, fetched by
  scientific name. For an aeroplane, a passing truck or a thunderclap that name
  is "vehicle" or "thunderstorm", the lookup finds nothing, and the row falls
  back to a grey bird silhouette. An operator scrolling the dashboard sees a
  column of identical grey birds where the interesting half of the station's
  detections are.

  So: a photograph of the actual aircraft when an authority named one, and the
  family's icon otherwise. The icon is not a placeholder for a missing
  photograph - for a truck or a chainsaw there is no photograph to miss, and an
  icon that says "road vehicle" is the whole honest answer.

  The photograph comes from lib/soundnet/aircraftPhoto, which holds the terms it
  is fetched under and caches a hex code across every row on the page.
-->
<script lang="ts">
  import {
    Plane,
    Car,
    CloudLightning,
    Siren,
    Wrench,
    Train,
    Ship,
    Music,
    Cat,
    Zap,
  } from '@lucide/svelte';
  import { aircraftPhoto, type AircraftPhoto } from '../aircraftPhoto';

  interface Aircraft {
    hex: string;
    registration?: string;
    typeCode?: string;
  }

  interface Props {
    /** The event domain this detection belongs to. */
    domain: string;
    /** The aircraft an authority named, when one did. */
    aircraft?: Aircraft | null;
    alt?: string;
    className?: string;
  }

  let { domain, aircraft = null, alt = '', className = '' }: Props = $props();

  let photo = $state<AircraftPhoto | null>(null);

  $effect(() => {
    const hex = aircraft?.hex;
    photo = null;
    if (!hex) return;

    let current = true;
    aircraftPhoto(hex).then(found => {
      if (current) photo = found;
    });
    return () => {
      current = false;
    };
  });

  // A Map, not a plain object: the domain arrives from an API response, and
  // indexing an object with a dynamic key can reach Object.prototype.
  const ICONS = new Map<string, typeof Plane>([
    ['aircraft', Plane],
    ['vehicle', Car],
    ['weather', CloudLightning],
    ['alarm', Siren],
    ['tool', Wrench],
    ['rail', Train],
    ['watercraft', Ship],
    ['music', Music],
    ['biological', Cat],
    ['impulse', Zap],
  ]);

  const Icon = $derived(ICONS.get(domain) ?? Zap);
</script>

{#if photo}
  <img src={photo.src} {alt} class="w-full h-full object-cover {className}" loading="lazy" />
{:else}
  <span
    class="w-full h-full flex items-center justify-center bg-base-200 {className}"
    role="img"
    aria-label={alt || domain}
  >
    <Icon class="h-1/2 w-1/2 opacity-50" aria-hidden="true" />
  </span>
{/if}
