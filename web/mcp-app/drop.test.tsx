// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { FileDropCard, isFileDrop, toBase64, type AppLike, type DropInfo } from './drop';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const INFO: DropInfo = { kind: 'file_drop', accept: ['.stl', '.obj', '.3mf', '.step', '.stp'], max_upload_bytes: 512 << 20, chunk_bytes: 72_000, meta: { collection: 'Envois' } };
const SHA = 'ab'.repeat(32);
const hash = async () => SHA;

let host: HTMLElement; let root: Root;
beforeEach(() => { host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host); });
afterEach(() => { act(() => root.unmount()); host.remove(); });

const settle = async (n = 12) => { for (let i = 0; i < n; i++) await act(async () => { await new Promise((r) => setTimeout(r, 0)); }); };
const fileOf = (size: number, name = 'cube.stl') => new File([new Uint8Array(size).map((_, i) => i % 251)], name);

/** Serveur simulé de drop_file_chunk : reçoit des morceaux, répond comme le vrai (in_progress puis complete). */
function mkApp(opts: { failFirst?: number; reject?: string; hold?: boolean } = {}) {
  const calls: Array<Record<string, unknown>> = [];
  let received = 0, total = 0, failed = 0;
  const waiting: Array<() => void> = [];
  const app: AppLike = {
    callServerTool: vi.fn(async (p) => {
      const a = p.arguments; calls.push(a);
      if (p.name !== 'drop_file_chunk') throw new Error('outil inattendu ' + p.name);
      if (opts.reject) return { isError: true, content: [{ type: 'text', text: opts.reject }] };
      if (failed < (opts.failFirst ?? 0)) { failed++; throw new Error('réseau coupé'); }
      if (opts.hold && calls.length === 2) await new Promise<void>((r) => waiting.push(r));
      if (a.total_size) total = a.total_size as number;
      const off = a.offset as number;
      const bytes = atob(a.data_base64 as string).length;
      if (off > received) return { structuredContent: { upload_id: 'upl_1', status: 'in_progress', next_offset: received, received, total_size: total, note: 'offset incorrect' } };
      received = Math.max(received, off + bytes);
      if (received >= total) return { structuredContent: { upload_id: 'upl_1', status: 'complete', received, total_size: total, next_offset: received, percent: 100, file: { status: 'imported', model_id: 'mdl_42', note: 'Modèle créé.' } } };
      return { structuredContent: { upload_id: 'upl_1', status: 'in_progress', received, total_size: total, next_offset: received, percent: Math.floor((received * 100) / total) } };
    }),
  };
  return { app, calls, release: () => waiting.shift()?.() };
}

const dropFile = async (f: File) => {
  const zone = host.querySelector('[role=button]') as HTMLElement;
  await act(async () => {
    const ev = new Event('drop', { bubbles: true, cancelable: true });
    Object.defineProperty(ev, 'dataTransfer', { value: { files: [f] } });
    zone.dispatchEvent(ev);
  });
  await settle();
};

describe('FileDropCard (glisser-déposer simulé)', () => {
  it('reconnaît le résultat de show_file_drop ; base64 sans perte', () => {
    expect(isFileDrop(INFO)).toBe(true); expect(isFileDrop({ kind: 'other' })).toBe(false);
    const b = new Uint8Array(70_000).map((_, i) => (i * 7) % 256);
    const back = Uint8Array.from(atob(toBase64(b)), (c) => c.charCodeAt(0));
    expect(back.length).toBe(b.length); expect(back.every((v, i) => v === b[i])).toBe(true);
  });

  it('envoie le fichier déposé par morceaux de ≤ 72 000 octets, puis propose l\'aperçu de la pièce', async () => {
    const { app, calls } = mkApp();
    const preview = vi.fn((id: string) => <div data-testid="preview">carte de {id}</div>);
    await act(async () => { root.render(<FileDropCard app={app} info={INFO} hash={hash} renderPreview={preview} />); });
    await dropFile(fileOf(200_000));

    expect(calls.length).toBe(3); // 72 000 + 72 000 + 56 000
    expect(calls[0]).toMatchObject({ filename: 'cube.stl', total_size: 200_000, sha256: SHA, offset: 0, collection: 'Envois' });
    expect(calls[1]).toMatchObject({ upload_id: 'upl_1', offset: 72_000 }); expect(calls[1].filename).toBeUndefined();
    expect(calls[2]).toMatchObject({ upload_id: 'upl_1', offset: 144_000 });
    for (const c of calls) expect((c.data_base64 as string).length).toBeLessThanOrEqual(100_000);
    expect(host.querySelector('[role=status]')!.textContent).toContain('importé');
    expect(host.querySelector('[data-testid=preview]')!.textContent).toBe('carte de mdl_42');
    expect(preview).toHaveBeenCalledWith('mdl_42');
  });

  it('barre de progression pendant l\'envoi', async () => {
    const { app, release } = mkApp({ hold: true });
    await act(async () => { root.render(<FileDropCard app={app} info={INFO} hash={hash} />); });
    await dropFile(fileOf(200_000));
    const bar = () => host.querySelector('progress') as HTMLProgressElement;
    expect(bar()).not.toBeNull(); expect(bar().value).toBe(36); // 72 000 / 200 000 après le 1er morceau (le 2e est retenu)
    expect(host.textContent).toContain('cube.stl : 36 %');
    // pas de second dépôt pendant un envoi
    expect((host.querySelector('[role=button]') as HTMLElement).getAttribute('aria-disabled')).toBe('true');
    await act(async () => { release(); });
    await settle();
    expect(host.querySelector('progress')).toBeNull();
    expect(host.textContent).toContain('importé');
  });

  it('un morceau perdu est renvoyé (3 essais) ; un refus définitif (empreinte, signature) ne l\'est pas', async () => {
    const a = mkApp({ failFirst: 2 });
    await act(async () => { root.render(<FileDropCard app={a.app} info={INFO} hash={hash} />); });
    await dropFile(fileOf(1000));
    expect(a.calls.length).toBe(3); expect(host.textContent).toContain('importé');

    act(() => root.unmount()); root = createRoot(host);
    const b = mkApp({ reject: 'sha256 incorrect : le contenu reçu ne correspond pas' });
    await act(async () => { root.render(<FileDropCard app={b.app} info={INFO} hash={hash} />); });
    await dropFile(fileOf(1000));
    expect(b.calls.length).toBe(1);
    expect(host.querySelector('[role=alert]')!.textContent).toContain('sha256 incorrect');
  });

  it('refuse sans rien envoyer : mauvais format, fichier vide, fichier trop gros', async () => {
    const { app, calls } = mkApp();
    await act(async () => { root.render(<FileDropCard app={app} info={{ ...INFO, max_upload_bytes: 100_000 }} hash={hash} />); });
    await dropFile(fileOf(100, 'script.exe'));
    expect(host.querySelector('[role=alert]')!.textContent).toContain('Format non pris en charge');
    await dropFile(fileOf(0));
    expect(host.querySelector('[role=alert]')!.textContent).toContain('vide');
    await dropFile(fileOf(150_000));
    expect(host.querySelector('[role=alert]')!.textContent).toContain('trop gros');
    expect(calls.length).toBe(0);
  });

  it('choix par le sélecteur de fichier et dépôt au clavier', async () => {
    const { app, calls } = mkApp();
    await act(async () => { root.render(<FileDropCard app={app} info={INFO} hash={hash} />); });
    const input = host.querySelector('[data-testid=fd-input]') as HTMLInputElement;
    expect(input.getAttribute('accept')).toBe('.stl,.obj,.3mf,.step,.stp');
    Object.defineProperty(input, 'files', { value: [fileOf(500, 'a.3mf')], configurable: true });
    await act(async () => { input.dispatchEvent(new Event('change', { bubbles: true })); });
    await settle();
    expect(calls.length).toBe(1); expect(calls[0].filename).toBe('a.3mf');
    const click = vi.spyOn(input, 'click');
    await act(async () => { (host.querySelector('[role=button]') as HTMLElement).dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })); });
    expect(click).toHaveBeenCalled();
  });

  it('reprise : si le serveur redonne un offset plus bas, le morceau est renvoyé à cet offset', async () => {
    let n = 0; let received = 0;
    const calls: Array<Record<string, unknown>> = [];
    const app: AppLike = { callServerTool: async (p) => {
      const a = p.arguments; calls.push(a); n++;
      const bytes = atob(a.data_base64 as string).length;
      if (n === 2) return { structuredContent: { upload_id: 'u', status: 'in_progress', next_offset: 72_000 } }; // réponse « décalée » : le 2e morceau est à refaire
      received = (a.offset as number) + bytes;
      return { structuredContent: { upload_id: 'u', status: received >= 150_000 ? 'complete' : 'in_progress', next_offset: received, file: received >= 150_000 ? { status: 'imported', model_id: 'm' } : undefined } };
    } };
    await act(async () => { root.render(<FileDropCard app={app} info={INFO} hash={hash} />); });
    await dropFile(fileOf(150_000));
    expect(calls.map((c) => c.offset)).toEqual([0, 72_000, 72_000, 144_000]);
    expect(host.textContent).toContain('importé');
  });
});
