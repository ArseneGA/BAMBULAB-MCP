import { afterEach, describe, expect, it, vi } from 'vitest';
import { CHUNK_BYTES, isSupported, uploadFile } from './upload';

describe('envoi de fichiers par morceaux', () => {
  afterEach(() => vi.unstubAllGlobals());

  const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

  it('reconnaît les formats pris en charge', () => {
    expect(isSupported('a.STL')).toBe(true);
    expect(isSupported('a.3mf')).toBe(true);
    expect(isSupported('a.exe')).toBe(false);
  });

  it('envoie les morceaux avec cookie + CSRF, reprend à l\'offset du serveur et termine', async () => {
    const size = CHUNK_BYTES * 2 + 10;
    const file = new File([new Uint8Array(size)], 'piece.stl');
    const calls: Array<{ method: string; url: string; csrf: string | undefined; credentials?: RequestCredentials }> = [];
    let first = true;
    const f = vi.fn(async (url: string, init: RequestInit) => {
      calls.push({ method: init.method!, url, csrf: (init.headers as Record<string, string>)['X-Atelier-Request'], credentials: init.credentials });
      if (init.method === 'POST' && url.endsWith('/api/uploads')) return json(201, { upload_id: 'upl_1', next_offset: 0 });
      if (init.method === 'PUT') {
        const off = Number(new URL(url, 'http://x').searchParams.get('offset'));
        if (off === CHUNK_BYTES && first) { // décalage simulé : le serveur a perdu le premier morceau : il demande de repartir de 0
          first = false;
          return json(409, { error: 'offset', code: 'offset_mismatch', status: { next_offset: 0 } });
        }
        return json(200, { upload_id: 'upl_1', next_offset: Math.min(off + CHUNK_BYTES, size), complete: off + CHUNK_BYTES >= size });
      }
      if (url.endsWith('/finish')) return json(200, { file: { status: 'imported', model_id: 'mdl_1' } });
      return json(200, {});
    });
    vi.stubGlobal('fetch', f);
    const progress: number[] = [];
    const r = await uploadFile(file, { collection: 'Web' }, (sent) => progress.push(sent));
    expect(r.file.model_id).toBe('mdl_1');
    expect(progress.at(-1)).toBe(size);
    expect(calls.every((c) => c.csrf === '1' && c.credentials === 'same-origin')).toBe(true);
    expect(calls.filter((c) => c.method === 'PUT').length).toBeGreaterThanOrEqual(3);
  });

  it('refuse un format inconnu sans appeler le serveur et nettoie la session en cas d\'échec', async () => {
    const f = vi.fn();
    vi.stubGlobal('fetch', f);
    await expect(uploadFile(new File(['x'], 'virus.exe'), {}, () => undefined)).rejects.toThrow(/Format/);
    expect(f).not.toHaveBeenCalled();

    const calls: string[] = [];
    vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit) => {
      calls.push(`${init.method} ${url}`);
      if (url.endsWith('/api/uploads')) return json(201, { upload_id: 'upl_2', next_offset: 0 });
      if (init.method === 'PUT') return json(400, { error: 'trop gros', code: 'invalid' });
      return json(200, {});
    }));
    await expect(uploadFile(new File(['abc'], 'a.stl'), {}, () => undefined)).rejects.toThrow('trop gros');
    expect(calls.some((c) => c.startsWith('DELETE'))).toBe(true);
  });
});
