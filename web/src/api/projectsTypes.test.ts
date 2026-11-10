import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from './client';
import { projectsApi } from './projectsTypes';

describe('API suivi de projet', () => {
  afterEach(() => vi.unstubAllGlobals());
  const stub = (status = 200, body: unknown = {}) => {
    const f = vi.fn(async () => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }));
    vi.stubGlobal('fetch', f);
    return f;
  };
  const call = (f: ReturnType<typeof stub>) => f.mock.calls[0] as unknown as [string, RequestInit];

  it('liste et fiche : lecture seule avec les en-têtes de session', async () => {
    const f = stub(200, { projects: [], total: 0, counts_by_status: {}, note: '' });
    await projectsApi.list({ status: 'printing', includeArchived: true });
    const [url, init] = call(f);
    expect(url).toBe('/api/projects?status=printing&include_archived=true');
    expect(init.method).toBe('GET');
    expect((init.headers as Record<string, string>)['X-Atelier-Request']).toBe('1');
    const g = stub();
    await projectsApi.get('prj_1');
    expect(call(g)[0]).toBe('/api/projects/prj_1');
    const p = stub();
    await projectsApi.pending();
    expect(call(p)[0]).toBe('/api/verdicts/pending');
  });

  it('verdict : POST JSON sur l\'impression, sans champ vide', async () => {
    const f = stub();
    await projectsApi.setVerdict('pj_1', 'failed', 'warping');
    const [url, init] = call(f);
    expect(url).toBe('/api/prints/pj_1/verdict');
    expect(init.method).toBe('POST');
    expect(JSON.parse(init.body as string)).toEqual({ verdict: 'failed', reason: 'warping' });
    const g = stub();
    await projectsApi.setVerdict('pj_1', 'success');
    expect(JSON.parse(call(g)[1].body as string)).toEqual({ verdict: 'success' });
  });

  it('remonte une erreur de validation et un service injoignable', async () => {
    stub(400, { error: 'entrée invalide : raison inconnue', code: 'invalid' });
    const err = await projectsApi.setVerdict('pj_1', 'failed', 'magie').catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).message).toContain('raison inconnue');
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('network'); }));
    expect(((await projectsApi.list().catch((e: unknown) => e)) as ApiError).offline).toBe(true);
  });
});
