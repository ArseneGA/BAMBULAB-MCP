import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from './client';
import { insightsApi } from './insights';

describe('API coût / journal / tableau de bord', () => {
  afterEach(() => vi.unstubAllGlobals());
  const stub = (status = 200, body: unknown = {}) => {
    const f = vi.fn(async () => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }));
    vi.stubGlobal('fetch', f);
    return f;
  };
  const call = (f: ReturnType<typeof stub>) => f.mock.calls[0] as unknown as [string, RequestInit];

  it('lit le tableau de bord avec la période en paramètres et les en-têtes de session', async () => {
    const f = stub();
    await insightsApi.dashboard({ from: '2026-11-01', to: '2026-11-30', printer_id: '' });
    const [url, init] = call(f);
    expect(url).toBe('/api/usage/dashboard?from=2026-11-01&to=2026-11-30');
    expect(init.credentials).toBe('same-origin');
    expect((init.headers as Record<string, string>)['X-Atelier-Request']).toBe('1');
  });

  it('envoie une photo en corps brut avec son type, sans JSON', async () => {
    const f = stub(201, { photo: 'a'.repeat(64), entry: {} });
    const file = new File([new Uint8Array([0x89, 0x50, 0x4e, 0x47])], 'p.png', { type: 'image/png' });
    await insightsApi.addPhoto('log_1', file);
    const [url, init] = call(f);
    expect(url).toBe('/api/print-log/log_1/photo');
    expect(init.method).toBe('POST');
    expect((init.headers as Record<string, string>)['Content-Type']).toBe('image/png');
    expect(init.body).toBe(file);
  });

  it('sérialise la mise à jour d\'une fiche en PATCH JSON', async () => {
    const f = stub();
    await insightsApi.updateLog('log_1', { rating: 4, defects: ['stringing'] });
    const [url, init] = call(f);
    expect(url).toBe('/api/print-log/log_1');
    expect(init.method).toBe('PATCH');
    expect(JSON.parse(init.body as string)).toEqual({ rating: 4, defects: ['stringing'] });
  });

  it('remonte une erreur de validation comme ApiError avec son message', async () => {
    stub(400, { error: 'entrée invalide : rating : entre 1 et 5', code: 'invalid' });
    const err = await insightsApi.updateLog('log_1', { rating: 9 }).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(400);
    expect((err as ApiError).message).toContain('rating');
  });

  it('distingue un service injoignable', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('network'); }));
    const err = await insightsApi.settings().catch((e: unknown) => e);
    expect((err as ApiError).offline).toBe(true);
  });

  it('construit l\'URL d\'une photo de fiche', () => {
    expect(insightsApi.photoUrl('log_1', 'abc')).toBe('/api/print-log/log_1/photos/abc');
  });
});
