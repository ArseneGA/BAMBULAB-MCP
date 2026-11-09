import { useCallback, useRef, useState } from 'react';
import type { DragEvent, ReactNode } from 'react';
import { AlertTriangle, CheckCircle2, UploadCloud } from 'lucide-react';
import './drop.css';

/**
 * Carte « Déposer un fichier » (ui://bambu/drop.html, liée à show_file_drop) : zone de glisser-déposer qui envoie le fichier
 * 3D par morceaux de ≤ 72 000 octets (base64 ≤ 96 000 caractères) à l'outil réservé drop_file_chunk, avec barre de
 * progression, reprise à next_offset et empreinte sha256 calculée dans la vue ; puis l'aperçu de la pièce (viewer 3D).
 * Le serveur recalcule l'empreinte, contrôle la signature et la taille : la vue n'a aucune autorité sur le contenu.
 */

export interface AppLike {
  callServerTool(p: { name: string; arguments: Record<string, unknown> }): Promise<unknown>;
}
interface ToolResultLike { structuredContent?: unknown; content?: Array<{ type: string; text?: string }>; isError?: boolean }

export interface DropInfo {
  kind: 'file_drop'; accept: string[]; max_upload_bytes: number; chunk_bytes: number;
  meta?: Record<string, unknown>;
}
export function isFileDrop(s: unknown): s is DropInfo {
  const o = s as Partial<DropInfo> | undefined;
  return !!o && o.kind === 'file_drop' && Array.isArray(o.accept) && typeof o.chunk_bytes === 'number';
}

/** Taille maximale lue en mémoire par la vue (l'empreinte exige le fichier entier). */
export const BROWSER_MAX_BYTES = 128 << 20;

interface UploadOut { upload_id?: string; status?: string; next_offset?: number; received?: number; total_size?: number; percent?: number; note?: string; file?: { status?: string; model_id?: string; note?: string } }

const errText = (r: ToolResultLike, d: string) => (r.content ?? []).map((c) => c.text ?? '').join(' ').trim() || d;

export const toBase64 = (b: Uint8Array): string => {
  let s = '';
  for (let i = 0; i < b.length; i += 0x8000) s += String.fromCharCode(...b.subarray(i, i + 0x8000));
  return btoa(s);
};

export async function sha256Hex(buf: ArrayBuffer): Promise<string> {
  const d = await crypto.subtle.digest('SHA-256', buf);
  return Array.from(new Uint8Array(d), (x) => x.toString(16).padStart(2, '0')).join('');
}

const readBytes = (f: File): Promise<ArrayBuffer> => {
  if (typeof f.arrayBuffer === 'function') return f.arrayBuffer();
  return new Promise((res, rej) => { const r = new FileReader(); r.onload = () => res(r.result as ArrayBuffer); r.onerror = () => rej(r.error); r.readAsArrayBuffer(f); });
};

const mb = (n: number) => (n / (1 << 20)).toFixed(n >= 10 << 20 ? 0 : 1);

type Phase =
  | { k: 'idle' } | { k: 'hash'; name: string }
  | { k: 'send'; name: string; done: number; total: number } | { k: 'done'; name: string; status: string; modelId?: string; note?: string }
  | { k: 'error'; name: string; message: string };

export function FileDropCard({ app, info, hash = sha256Hex, renderPreview }: { app: AppLike; info: DropInfo; hash?: (b: ArrayBuffer) => Promise<string>; renderPreview?: (modelId: string) => ReactNode }) {
  const [phase, setPhase] = useState<Phase>({ k: 'idle' });
  const [over, setOver] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  const busy = phase.k === 'hash' || phase.k === 'send';
  const max = Math.min(info.max_upload_bytes || BROWSER_MAX_BYTES, BROWSER_MAX_BYTES);
  const accept = info.accept.join(',');

  const call = useCallback(async (args: Record<string, unknown>): Promise<UploadOut> => {
    let last: Error = new Error('Envoi interrompu');
    for (let attempt = 0; attempt < 3; attempt++) { // un morceau perdu est renvoyé tel quel (le serveur ignore un morceau déjà reçu)
      try {
        const r = (await app.callServerTool({ name: 'drop_file_chunk', arguments: args })) as ToolResultLike;
        if (r.isError) throw new Error(errText(r, 'Morceau refusé'));
        return (r.structuredContent ?? {}) as UploadOut;
      } catch (e) {
        last = e as Error;
        if (/sha256|signature|trop gros|format|extension|nom de fichier/i.test(last.message)) break; // refus définitif : inutile de réessayer
      }
    }
    throw last;
  }, [app]);

  const send = useCallback(async (file: File) => {
    const name = file.name;
    const ext = /\.[^.]+$/.exec(name)?.[0].toLowerCase() ?? '';
    if (!info.accept.includes(ext)) { setPhase({ k: 'error', name, message: `Format non pris en charge (${ext || 'sans extension'}) : ${info.accept.join(' ')}` }); return; }
    if (file.size === 0) { setPhase({ k: 'error', name, message: 'Fichier vide.' }); return; }
    if (file.size > max) { setPhase({ k: 'error', name, message: `Fichier trop gros (${mb(file.size)} Mo) : ${mb(max)} Mo au plus depuis cette carte. Utilisez la page Importer de l'interface web.` }); return; }
    try {
      setPhase({ k: 'hash', name });
      const buf = await readBytes(file);
      const sha = await hash(buf);
      const bytes = new Uint8Array(buf);
      const chunk = Math.max(1024, Math.min(info.chunk_bytes || 72_000, 75_000));
      let id: string | undefined, offset = 0, stall = 0, out: UploadOut = {};
      setPhase({ k: 'send', name, done: 0, total: bytes.length });
      while (offset < bytes.length) {
        const end = Math.min(bytes.length, offset + chunk);
        const data = toBase64(bytes.subarray(offset, end));
        out = await call(id
          ? { upload_id: id, offset, data_base64: data }
          : { filename: name, total_size: bytes.length, sha256: sha, offset: 0, data_base64: data, ...(info.meta ?? {}) });
        id = out.upload_id ?? id;
        if (out.status === 'complete') { offset = bytes.length; break; }
        // reprise : le serveur donne le bon offset (réponse perdue, morceau déjà reçu…)
        const prev = offset;
        offset = Math.min(typeof out.next_offset === 'number' ? out.next_offset : end, bytes.length);
        if (offset === prev && ++stall > 3) throw new Error('Le serveur ne progresse pas : envoi abandonné.');
        setPhase({ k: 'send', name, done: offset, total: bytes.length });
      }
      if (out.status !== 'complete') throw new Error('Le serveur n\'a pas confirmé la fin de l\'envoi.');
      setPhase({ k: 'done', name, status: out.file?.status ?? 'imported', modelId: out.file?.model_id, note: out.file?.note ?? out.note });
    } catch (e) { setPhase({ k: 'error', name, message: (e as Error).message || 'Envoi interrompu' }); }
  }, [call, hash, info, max]);

  const onDrop = (ev: DragEvent) => {
    ev.preventDefault(); setOver(false);
    const f = ev.dataTransfer?.files?.[0];
    if (f && !busy) void send(f);
  };
  const dragOver = (ev: DragEvent) => { ev.preventDefault(); if (!busy) setOver(true); };

  const pct = phase.k === 'send' ? Math.floor((phase.done / Math.max(1, phase.total)) * 100) : phase.k === 'done' ? 100 : 0;
  return (
    <section className="fd" aria-label="Déposer un fichier">
      <header className="fd-head"><h1>Déposer un fichier 3D</h1><span className="fd-sub">{info.accept.join(' ')} · {mb(max)} Mo au plus</span></header>
      <div
        className={['fd-zone', over && 'fd-over', busy && 'fd-busy'].filter(Boolean).join(' ')}
        role="button" tabIndex={0} aria-label="Zone de dépôt : glisser un fichier ici ou appuyer pour choisir" aria-disabled={busy}
        onClick={() => { if (!busy) input.current?.click(); }}
        onKeyDown={(e) => { if ((e.key === 'Enter' || e.key === ' ') && !busy) { e.preventDefault(); input.current?.click(); } }}
        onDragEnter={dragOver} onDragOver={dragOver} onDragLeave={() => setOver(false)} onDrop={onDrop}
      >
        <UploadCloud size={28} aria-hidden />
        <span>{busy ? 'Envoi en cours…' : 'Glissez un fichier ici ou cliquez pour le choisir'}</span>
        <input ref={input} type="file" accept={accept} hidden data-testid="fd-input" onChange={(e) => { const f = e.target.files?.[0]; e.target.value = ''; if (f && !busy) void send(f); }} />
      </div>

      {(phase.k === 'hash' || phase.k === 'send') && (
        <div className="fd-prog" role="status" aria-live="polite">
          <progress max={100} value={pct} aria-label={`Envoi de ${phase.name}`} />
          <span>{phase.k === 'hash' ? `Calcul de l'empreinte de ${phase.name}…` : `${phase.name} : ${pct} % (${mb(phase.done)} / ${mb(phase.total)} Mo)`}</span>
        </div>
      )}
      {phase.k === 'error' && <p className="fd-err" role="alert"><AlertTriangle size={14} aria-hidden /> {phase.name} : {phase.message}</p>}
      {phase.k === 'done' && (
        <>
          <p className="fd-ok" role="status"><CheckCircle2 size={14} aria-hidden /> {phase.name} : {({ imported: 'importé', new_version: 'nouvelle version ajoutée', duplicate: 'déjà présent (aucun doublon créé)', unchanged: 'inchangé' } as Record<string, string>)[phase.status] ?? phase.status}.{phase.note ? ` ${phase.note}` : ''}</p>
          {phase.modelId && renderPreview ? <div className="fd-preview">{renderPreview(phase.modelId)}</div> : null}
        </>
      )}
    </section>
  );
}
