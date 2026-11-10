import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from './client';
import { issuesApi } from './issues';

describe('API carnet des dysfonctionnements', () => {
  afterEach(() => vi.unstubAllGlobals());
  const stub = (status = 200, body: unknown = {}, text?: string) => {
    const f = vi.fn(async () => new Response(text ?? JSON.stringify(body), { status, headers: { 'Content-Type': text ? 'text/markdown' : 'application/json' } }));
    vi.stubGlobal('fetch', f);
    return f;
  };
  const call = (f: ReturnType<typeof stub>) => f.mock.calls[0] as unknown as [string, RequestInit];

  it('liste avec les filtres en paramètres, sans les vides, avec les en-têtes de session', async () => {
    const f = stub(200, { issues: [], total: 0, counts: {} });
    await issuesApi.list({ state: 'open', severity: 'P1', tool: '' });
    const [url, init] = call(f);
    expect(url).toBe('/api/issues?state=open&severity=P1');
    expect(init.credentials).toBe('same-origin');
    expect((init.headers as Record<string, string>)['X-Atelier-Request']).toBe('1');
  });

  it('change l\'état en PATCH JSON avec la référence du commit', async () => {
    const f = stub();
    await issuesApi.update('iss_1', { state: 'fixed', fix_commit: 'abc1234' });
    const [url, init] = call(f);
    expect(url).toBe('/api/issues/iss_1');
    expect(init.method).toBe('PATCH');
    expect(JSON.parse(init.body as string)).toEqual({ state: 'fixed', fix_commit: 'abc1234' });
  });

  it('exporte le Markdown comme texte brut', async () => {
    const f = stub(200, undefined, '# Dysfonctionnements de Printalk\n');
    const md = await issuesApi.exportMarkdown({ state: 'all' });
    expect(md).toBe('# Dysfonctionnements de Printalk\n');
    expect(call(f)[0]).toBe('/api/issues/export?state=all');
  });

  it('remonte une erreur de validation comme ApiError avec son message', async () => {
    stub(400, { error: 'entrée invalide : fix_commit requis', code: 'invalid' });
    const err = await issuesApi.update('iss_1', { state: 'fixed' }).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(400);
    expect((err as ApiError).message).toContain('fix_commit');
  });

  it('distingue un service injoignable', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('network'); }));
    const err = await issuesApi.list({}).catch((e: unknown) => e);
    expect((err as ApiError).offline).toBe(true);
  });
});
