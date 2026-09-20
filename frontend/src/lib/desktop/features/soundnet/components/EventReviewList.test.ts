import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, waitFor, fireEvent } from '@testing-library/svelte';

import EventReviewList from './EventReviewList.svelte';
import { navigation } from '$lib/stores/navigation.svelte';

vi.mock('$lib/stores/navigation.svelte', () => ({
  navigation: { currentPath: '/ui/events', navigate: vi.fn(), handlePopState: vi.fn() },
}));

// The shared setup mocks `t` to echo the key.

const originalFetch = globalThis.fetch;

afterEach(() => {
  cleanup();
  globalThis.fetch = originalFetch;
  vi.clearAllMocks();
});

interface Row {
  id: number;
  date: string;
  time: string;
  commonName: string;
  scientificName: string;
  eventDisplayName?: string;
  confidence: number;
  verified: 'correct' | 'false_positive' | 'unverified';
  passId?: number;
  resolvedDomain?: string;
}

function respondWith(rows: Row[]) {
  globalThis.fetch = vi.fn().mockResolvedValue({
    ok: true,
    status: 200,
    json: () => Promise.resolve(rows),
  }) as unknown as typeof fetch;
}

function row(overrides: Partial<Row>): Row {
  return {
    id: 1,
    date: '2026-09-21',
    time: '08:00:00',
    commonName: 'Vehicle',
    scientificName: 'vehicle',
    eventDisplayName: 'Vehicle',
    confidence: 0.5,
    verified: 'unverified',
    ...overrides,
  };
}

describe('EventReviewList', () => {
  // On the operator's word: the species models are mature and birds have their
  // own review flow. This list is for what the new models are still learning.
  it('leaves birds out', async () => {
    respondWith([
      row({ id: 1, eventDisplayName: 'Aircraft', commonName: 'Aircraft' }),
      row({ id: 2, eventDisplayName: undefined, commonName: 'Willie-wagtail' }),
    ]);

    render(EventReviewList);

    await waitFor(() => expect(screen.getByText('Aircraft')).toBeInTheDocument());
    expect(screen.queryByText('Willie-wagtail')).not.toBeInTheDocument();
  });

  it('leaves out what has already been reviewed', async () => {
    respondWith([
      row({ id: 1, eventDisplayName: 'Aircraft', commonName: 'Aircraft' }),
      row({
        id: 2,
        eventDisplayName: 'Thunder',
        commonName: 'Thunder',
        verified: 'false_positive',
      }),
    ]);

    render(EventReviewList);

    await waitFor(() => expect(screen.getByText('Aircraft')).toBeInTheDocument());
    expect(screen.queryByText('Thunder')).not.toBeInTheDocument();
  });

  // One aeroplane makes several rows in half a minute; reviewing it six times
  // teaches nothing the first review did not. The row shown is one whose own
  // class already names the right thing, not the corrected Vehicle row.
  it('shows one row per pass, preferring one that names itself correctly', async () => {
    respondWith([
      row({
        id: 10,
        passId: 10,
        eventDisplayName: 'Vehicle',
        confidence: 0.74,
        resolvedDomain: 'aircraft',
      }),
      row({
        id: 11,
        passId: 10,
        eventDisplayName: 'Fixed-wing aircraft, airplane',
        confidence: 0.26,
      }),
      row({
        id: 12,
        passId: 10,
        eventDisplayName: 'Vehicle',
        confidence: 0.47,
        resolvedDomain: 'aircraft',
      }),
    ]);

    render(EventReviewList);

    await waitFor(() =>
      expect(screen.getByText('Fixed-wing aircraft, airplane')).toBeInTheDocument()
    );
    expect(screen.queryByText('Vehicle')).not.toBeInTheDocument();
  });

  // The whole point of the change: hand off to the review the operator already
  // uses, which shows the spectrogram.
  it('opens the built-in review for the chosen detection', async () => {
    respondWith([row({ id: 1917, eventDisplayName: 'Vehicle' })]);

    render(EventReviewList);

    const button = await screen.findByRole('button', { name: /Vehicle/ });
    await fireEvent.click(button);
    expect(navigation.navigate).toHaveBeenCalledWith('/ui/detections/1917?tab=review');
  });

  it('says so when there is nothing to review', async () => {
    respondWith([row({ id: 1, verified: 'correct' })]);

    render(EventReviewList);

    await waitFor(() =>
      expect(screen.getByText('soundnet.events.reviewEmpty')).toBeInTheDocument()
    );
  });

  // An empty list and a failed request must not look alike.
  it('reports a failed request rather than showing an empty list', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 500,
      json: () => Promise.resolve({}),
    }) as unknown as typeof fetch;

    render(EventReviewList);

    await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument());
    expect(screen.queryByText('soundnet.events.reviewEmpty')).not.toBeInTheDocument();
  });
});
