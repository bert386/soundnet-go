import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, waitFor } from '@testing-library/svelte';

import SoundNetPanel from './SoundNetPanel.svelte';

// The shared setup mocks `t` to echo the key, so assertions match i18n keys.

const originalFetch = globalThis.fetch;

afterEach(() => {
  cleanup();
  globalThis.fetch = originalFetch;
});

interface PanelResponse {
  detectionId: number;
  domain: string;
  candidateDomains?: string[];
  resolvedDomain?: string;
  diagnosable: boolean;
  enrichable: boolean;
  reviewState: string;
}

function respondWith(body: PanelResponse) {
  globalThis.fetch = vi.fn().mockResolvedValue({
    ok: true,
    status: 200,
    json: () => Promise.resolve(body),
  }) as unknown as typeof fetch;
}

function base(overrides: Partial<PanelResponse> = {}): PanelResponse {
  return {
    detectionId: 1237,
    domain: 'vehicle',
    diagnosable: true,
    enrichable: true,
    reviewState: 'unreviewed',
    ...overrides,
  };
}

describe('SoundNetPanel domain resolution', () => {
  // The case that made this exist: AudioSet's Vehicle is the parent class of
  // Aircraft, so an airliner is recorded as road traffic. ADS-B has already
  // named the aircraft by the time an operator reads the row, and until this
  // was shown, the panel said "vehicle" and nothing else.
  it('leads with the resolved domain when an authority disagreed with the sound', async () => {
    respondWith(
      base({
        candidateDomains: ['vehicle', 'aircraft', 'rail', 'watercraft'],
        resolvedDomain: 'aircraft',
      })
    );

    render(SoundNetPanel, { props: { detectionId: '1237' } });

    await waitFor(() => {
      expect(screen.getByText(/soundnet\.domainResolved/)).toBeInTheDocument();
    });

    // Both readings stay visible. What the microphone heard is still what was
    // recorded, and an operator checking a surprising match needs to see it.
    const badge = screen.getByTitle('soundnet.domainResolvedHint');
    expect(badge).toHaveTextContent('vehicle');
    expect(badge).toHaveTextContent('aircraft');
  });

  // A resolution that agrees with the acoustic reading is not a correction and
  // must not be dressed as one.
  it('shows the plain domain when the resolution matches the classification', async () => {
    respondWith(base({ domain: 'aircraft', resolvedDomain: 'aircraft' }));

    render(SoundNetPanel, { props: { detectionId: '1225' } });

    await waitFor(() => {
      expect(screen.getByText(/soundnet\.domain/)).toBeInTheDocument();
    });
    expect(screen.queryByText(/soundnet\.domainResolved/)).not.toBeInTheDocument();
    expect(screen.queryByTitle('soundnet.domainResolvedHint')).not.toBeInTheDocument();
  });

  // Nothing was resolved, but the label still does not settle what made the
  // sound. Saying so is what explains why a vehicle detection was queried
  // against the sky at all.
  it('names the other candidate domains when nothing was resolved', async () => {
    respondWith(base({ candidateDomains: ['vehicle', 'aircraft', 'rail', 'watercraft'] }));

    render(SoundNetPanel, { props: { detectionId: '1236' } });

    await waitFor(() => {
      expect(screen.getByText(/soundnet\.domainAmbiguous/)).toBeInTheDocument();
    });
    expect(screen.queryByText(/soundnet\.domainResolved/)).not.toBeInTheDocument();
  });

  // A label that determines its own domain carries one candidate. It must not
  // produce an "it could also be" line with nothing after it.
  it('stays quiet for a label that determines its own domain', async () => {
    respondWith(base({ domain: 'aircraft', candidateDomains: ['aircraft'] }));

    render(SoundNetPanel, { props: { detectionId: '1225' } });

    await waitFor(() => {
      expect(screen.getByText(/soundnet\.domain/)).toBeInTheDocument();
    });
    expect(screen.queryByText(/soundnet\.domainAmbiguous/)).not.toBeInTheDocument();
  });
});
