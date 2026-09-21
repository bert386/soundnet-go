import { localizeScientific } from '$lib/stores/speciesDictionary.svelte';
import { resolveEventClass } from '$lib/stores/eventTaxonomy.svelte';

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
 *
 * SOUNDNET: where the server does not supply one - every analytics endpoint,
 * which returns species rows and knows nothing of events - the event taxonomy
 * is consulted for the same answer. That is what stops "and_airscrew" and
 * "car_(siren)" appearing on the species page, the summary and the dashboard,
 * all of which reach this one function. It returns null for a bird and null
 * until the taxonomy has loaded, so the behaviour is unchanged in both cases.
 */
export function localizeSpeciesName(
  scientificName: string | undefined,
  fallbackCommonName?: string,
  eventDisplayName?: string
): string {
  if (eventDisplayName) return eventDisplayName;

  const event = resolveEventClass(scientificName, fallbackCommonName);
  if (event) return event.label;

  if (scientificName) {
    const localized = localizeScientific(scientificName);
    if (localized) return localized;
  }
  return fallbackCommonName ?? scientificName ?? '';
}
