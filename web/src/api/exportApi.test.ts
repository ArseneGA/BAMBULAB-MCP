import { afterEach, describe, expect, it, vi } from 'vitest';
import { exportApi } from './exportApi';

afterEach(() => vi.unstubAllGlobals());

const reply = (status: number, body: unknown) => vi.fn(async () => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }));

describe('exportApi', () => {
  it('lance un export avec l\'en-tête anti-CSRF et le corps JSON', async () => {
    const f = reply(202, { export_id: 'exp_1', job_id: 'job_1', plan: { title: 'P', parts: [], files: 0, total_bytes: 0, max_bytes: 1 } });
    vi.stubGlobal('fetch', f);
    const st = await exportApi.start({ collection_id: 'col_1', options: { license: 'CC BY 4.0', include_gcode_3mf: false } });
    expect(st.export_id).toBe('exp_1');
    const [url, init] = f.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe('/api/exports');
    expect(init.method).toBe('POST');
    expect((init.headers as Record<string, string>)['X-Atelier-Request']).toBe('1');
    expect(JSON.parse(init.body as string)).toEqual({ collection_id: 'col_1', options: { license: 'CC BY 4.0', include_gcode_3mf: false } });
  });

  it('remonte l\'erreur du serveur (limite de taille, entrée invalide)', async () => {
    vi.stubGlobal('fetch', reply(400, { error: 'les fichiers originaux pèsent trop', code: 'invalid' }));
    await expect(exportApi.start({ collection_id: 'c' })).rejects.toMatchObject({ status: 400, code: 'invalid', message: 'les fichiers originaux pèsent trop' });
  });

  it('demande un lien signé puis en tire l\'adresse de téléchargement relative', async () => {
    vi.stubGlobal('fetch', reply(200, { url: 'http://h/api/exports/exp_1.zip?exp=1&sig=ab', path: '/api/exports/exp_1.zip?exp=1&sig=ab', expires_at: '2026-01-01T00:00:00Z', file_name: 'p.zip', size_bytes: 10 }));
    const l = await exportApi.link('exp_1');
    expect(exportApi.downloadUrl(l)).toBe('/api/exports/exp_1.zip?exp=1&sig=ab');
  });
});
