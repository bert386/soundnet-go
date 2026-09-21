<!--
  EventIcon.svelte - the domain badge shown where a bird photo would be

  A Cessna, a lawnmower and a thunderclap were all being given the same grey
  bird silhouette, in a list headed "45 species". The silhouette is the right
  placeholder for a bird whose photograph has not loaded; it is the wrong
  placeholder for something that is not a bird at all, and it is most of why the
  reporting reads as bird software with other things bolted on.

  Renders nothing for a species, so callers can drop it in beside the existing
  thumbnail without a branch.
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
    Bird,
    Zap,
  } from '@lucide/svelte';

  interface Props {
    domain: string | null;
    /** Tailwind size classes for the icon itself. */
    size?: string;
    className?: string;
  }

  let { domain, size = 'h-6 w-6', className = '' }: Props = $props();

  // A Map, not an object: the domain is a value from an API response, and
  // indexing a plain object with one can reach Object.prototype.
  const ICONS = new Map([
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

  // Colour by family, so a page can be read at a glance rather than by name.
  const TINTS = new Map([
    ['aircraft', 'text-sky-500'],
    ['vehicle', 'text-amber-500'],
    ['weather', 'text-indigo-400'],
    ['alarm', 'text-red-500'],
    ['tool', 'text-orange-500'],
    ['rail', 'text-emerald-500'],
    ['watercraft', 'text-cyan-500'],
    ['music', 'text-fuchsia-500'],
    ['biological', 'text-lime-500'],
    ['impulse', 'text-rose-500'],
  ]);

  const Icon = $derived(domain ? (ICONS.get(domain) ?? Zap) : null);
  const tint = $derived(domain ? (TINTS.get(domain) ?? 'text-base-content') : '');
</script>

{#if Icon}
  {@const Component = Icon}
  <span
    class="inline-flex items-center justify-center {className}"
    title={domain}
    aria-label={domain}
  >
    <Component class="{size} {tint}" aria-hidden="true" />
  </span>
{/if}
