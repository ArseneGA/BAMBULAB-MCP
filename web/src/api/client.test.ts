import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, api } from './client';

// Les appels d'API portent la session du navigateur (cookie) et l'en-tête CSRF X-Atelier-Request.
describe('client API', () => {
  afterEach(() => vi.unstubAllGlobals());

  const stub = (status = 200, body: unknown = {}) => {
    const f = vi.fn(async () => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }));
    vi.stubGlobal('fetch', f);
    return f;
  };
  const initOf = (f: ReturnType<typeof stub>) => (f.mock.calls[0] as unknown as [string, RequestInit])[1];

  it('envoie le cookie de même origine et X-Atelier-Request sur une lecture', async () => {
    const f = stub();
    await api.capabilities();
    const init = initOf(f);
    expect(init.credentials).toBe('same-origin');
    expect((init.headers as Record<string, string>)['X-Atelier-Request']).toBe('1');
  });

  it('envoie aussi l\'en-tête sur une écriture JSON', async () => {
    const f = stub();
    await api.cancelJob('job_1');
    const init = initOf(f);
    expect(init.method).toBe('POST');
    expect(init.credentials).toBe('same-origin');
    expect((init.headers as Record<string, string>)['X-Atelier-Request']).toBe('1');
  });

  it('remonte un 401 comme ApiError (le Shell affiche alors l\'écran d\'enregistrement)', async () => {
    stub(401, { error: 'jeton d\'API requis', code: 'unauthorized', enroll_url: '/enroll' });
    const err = await api.capabilities().catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(401);
    expect((err as ApiError).code).toBe('unauthorized');
  });

  it('l\'export groupé envoie aussi la session et l\'en-tête', async () => {
    const f = vi.fn(async () => new Response(new Blob(['zip']), { status: 200, headers: { 'X-Export-Files': '1' } }));
    vi.stubGlobal('fetch', f);
    await api.exportZip(['mdl_1']);
    const init = (f.mock.calls[0] as unknown as [string, RequestInit])[1];
    expect(init.credentials).toBe('same-origin');
    expect((init.headers as Record<string, string>)['X-Atelier-Request']).toBe('1');
  });
});
