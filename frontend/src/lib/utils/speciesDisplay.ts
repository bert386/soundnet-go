import { localizeScientific } from '$lib/stores/speciesDictionary.svelte';

/**
 * The species common name to show this visitor, in their UI locale.
 * Fallback chain: client dictionary (visitor locale) -> server-provided common
 * name (server locale) -> scientific name. Reactive: call inside $derived/$effect.
 *
 * SOUNDNET: eventDisplayName, when the server supplies one, wins outright. It is
 * only ever set for a non-bird event class, where the stored scientific/common
 * pair is two halves of a label the datastore split at an underscore
 * ("propeller" + "and_airscrew"). Neither half is a name worth showing, and
 * neither is in the species dictionary, so consulting it first would only risk a
 * truncated event name colliding with a real species.
 */
export function localizeSpeciesName(
  scientificName: string | undefined,
  fallbackCommonName?: string,
  eventDisplayName?: string
): string {
  if (eventDisplayName) return eventDisplayName;
  if (scientificName) {
    const localized = localizeScientific(scientificName);
    if (localized) return localized;
  }
  return fallbackCommonName ?? scientificName ?? '';
}
