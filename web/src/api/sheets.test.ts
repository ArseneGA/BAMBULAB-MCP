import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from './client';
import { sheetsApi, unavailableText } from './sheets';
import { PART_SHEET, PART_SHEET_EMPTY, PRINT_SHEET, PRINT_SHEET_NO_CAMERA } from './sheets.fixtures';

describe('API fiches pièce et impression', () => {
  afterEach(() => vi.unstubAllGlobals());
  const stub = (status = 200, body: unknown = {}) => {
    const f = vi.fn(async () => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }));
    vi.stubGlobal('fetch', f);
    return f;
  };
  const call = (f: ReturnType<typeof stub>) => f.mock.calls[0] as unknown as [string, RequestInit];

  it('fiche pièce : GET avec sections et limite, en-têtes de session', async () => {
    const f = stub(200, PART_SHEET);
    const s = await sheetsApi.part('mdl_sheet1', { include: ['versions', 'prints'], limit: 5 });
    const [url, init] = call(f);
    expect(url).toBe('/api/models/mdl_sheet1/sheet?include=versions%2Cprints&limit=5');
    expect(init.method).toBe('GET');
    expect((init.headers as Record<string, string>)['X-Atelier-Request']).toBe('1');
    expect(s.counts.printed).toBe(5);
    const g = stub(200, PART_SHEET);
    await sheetsApi.part('mdl 1/x');
    expect(call(g)[0]).toBe('/api/models/mdl%201%2Fx/sheet');
  });

  it('fiche d\'impression : GET sur l\'impression', async () => {
    const f = stub(200, PRINT_SHEET);
    const s = await sheetsApi.print('pj_5');
    expect(call(f)[0]).toBe('/api/prints/pj_5/sheet');
    expect(s.profile.frozen_note).toMatch(/ne changent pas/);
  });

  it('erreur : message du serveur et code', async () => {
    stub(404, { error: 'introuvable', code: 'not_found' });
    await expect(sheetsApi.print('nope')).rejects.toMatchObject({ status: 404, code: 'not_found' } satisfies Partial<ApiError>);
  });

  it('valeur absente : « non disponible » avec la raison, jamais 0', () => {
    expect(unavailableText(PRINT_SHEET_NO_CAMERA, 'actual_seconds')).toBe('non disponible (durée réelle non relevée)');
    expect(unavailableText(PRINT_SHEET, 'actual_seconds')).toBeUndefined();
    expect(PART_SHEET_EMPTY.counts.text).toBe('jamais imprimée');
    expect(PART_SHEET_EMPTY.prints).toEqual([]);
  });
});
