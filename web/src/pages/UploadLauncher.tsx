import { useCallback, useEffect, useRef, useState } from 'react';
import { Link2, Upload } from 'lucide-react';
import { Button, Field, Input, ProgressBar, Tag } from '../components';
import { describeError } from '../api/client';
import { ACCEPT, importFromUrl, isSupported, uploadFile, uploadLimits } from '../api/upload';
import type { UploadLimits, UploadedFile } from '../api/upload';
import { Dialog, Notice, fmtBytes } from './common';
import './upload.css';

type ItemState = 'wait' | 'sending' | 'done' | 'error';
interface Item { key: string; file: File; state: ItemState; sent: number; result?: UploadedFile; error?: string }

const STATUS_LABEL: Record<string, string> = {
  imported: 'Ajouté au catalogue', new_version: 'Nouvelle version', duplicate: 'Doublon : déjà au catalogue', unchanged: 'Identique : inchangé',
};

/**
 * Bouton « Envoyer des fichiers » + glisser-déposer sur toute la page Bibliothèque.
 * Envoi par morceaux (api/upload.ts) : barre de progression par fichier, résultat (modèle créé, version, doublon).
 */
export function UploadLauncher({ onUploaded }: { onUploaded: () => void }) {
  const [open, setOpen] = useState(false);
  const [items, setItems] = useState<Item[]>([]);
  const [drag, setDrag] = useState(false);
  const [busy, setBusy] = useState(false);
  const [collection, setCollection] = useState('');
  const [tags, setTags] = useState('');
  const [license, setLicense] = useState('');
  const [limits, setLimits] = useState<UploadLimits>();
  const [url, setUrl] = useState('');
  const [urlMsg, setUrlMsg] = useState<{ tone: 'err' | 'info'; text: string }>();
  const ac = useRef<AbortController | null>(null);
  const seq = useRef(0);

  useEffect(() => { const c = new AbortController(); uploadLimits(c.signal).then(setLimits).catch(() => undefined); return () => c.abort(); }, []);
  useEffect(() => () => ac.current?.abort(), []);

  const add = useCallback((files: File[]) => {
    if (files.length === 0) return;
    setItems((cur) => [...cur, ...files.map((file): Item => isSupported(file.name)
      ? { key: `f${++seq.current}`, file, state: 'wait', sent: 0 }
      : { key: `f${++seq.current}`, file, state: 'error', sent: 0, error: 'Format non pris en charge (STL, OBJ, 3MF, STEP).' })]);
    setOpen(true);
  }, []);

  // Glisser-déposer n'importe où sur la page (uniquement des fichiers).
  useEffect(() => {
    let depth = 0;
    const hasFiles = (e: DragEvent) => Array.from(e.dataTransfer?.types ?? []).includes('Files');
    const enter = (e: DragEvent) => { if (hasFiles(e)) { depth++; setDrag(true); } };
    const leave = (e: DragEvent) => { if (hasFiles(e) && --depth <= 0) { depth = 0; setDrag(false); } };
    const over = (e: DragEvent) => { if (hasFiles(e)) e.preventDefault(); };
    const drop = (e: DragEvent) => { if (!hasFiles(e)) return; e.preventDefault(); depth = 0; setDrag(false); add(Array.from(e.dataTransfer?.files ?? [])); };
    window.addEventListener('dragenter', enter); window.addEventListener('dragleave', leave);
    window.addEventListener('dragover', over); window.addEventListener('drop', drop);
    return () => { window.removeEventListener('dragenter', enter); window.removeEventListener('dragleave', leave); window.removeEventListener('dragover', over); window.removeEventListener('drop', drop); };
  }, [add]);

  const patch = (key: string, p: Partial<Item>) => setItems((cur) => cur.map((i) => (i.key === key ? { ...i, ...p } : i)));
  const meta = () => ({
    collection: collection.trim() || undefined, license: license.trim() || undefined,
    tags: tags.split(',').map((t) => t.trim()).filter(Boolean), source: 'envoi depuis le navigateur',
  });

  const start = async () => {
    setBusy(true);
    ac.current?.abort(); ac.current = new AbortController();
    let any = false;
    for (const it of items.filter((i) => i.state === 'wait' || (i.state === 'error' && isSupported(i.file.name)))) {
      if (ac.current.signal.aborted) break;
      patch(it.key, { state: 'sending', sent: 0, error: undefined });
      try {
        const r = await uploadFile(it.file, meta(), (sent) => patch(it.key, { sent }), ac.current.signal);
        patch(it.key, { state: 'done', sent: it.file.size, result: r.file });
        any = true;
      } catch (e) {
        if ((e as Error).name === 'AbortError') { patch(it.key, { state: 'wait' }); break; }
        patch(it.key, { state: 'error', error: describeError(e) });
      }
    }
    setBusy(false);
    if (any) onUploaded();
  };

  const doUrl = async () => {
    setBusy(true); setUrlMsg(undefined);
    ac.current?.abort(); ac.current = new AbortController();
    try {
      const r = await importFromUrl(url.trim(), meta(), ac.current.signal);
      setUrlMsg({ tone: 'info', text: `${r.files.length} fichier(s) depuis ${r.source}${r.license ? ` · licence ${r.license}` : ''} : ${r.files.map((f) => STATUS_LABEL[f.status] ?? f.status).join(', ')}.` });
      setUrl(''); onUploaded();
    } catch (e) {
      if ((e as Error).name !== 'AbortError') setUrlMsg({ tone: 'err', text: describeError(e) });
    } finally { setBusy(false); }
  };

  const close = () => { if (busy) ac.current?.abort(); setOpen(false); if (!busy) setItems((cur) => cur.filter((i) => i.state === 'wait')); };
  const pending = items.filter((i) => i.state === 'wait' || (i.state === 'error' && isSupported(i.file.name))).length;
  const max = limits ? fmtBytes(limits.max_upload_bytes) : undefined;
  const urlOn = limits?.url_import.enabled;

  return (
    <>
      <Button icon={<Upload size={15} />} onClick={() => setOpen(true)}>Envoyer des fichiers</Button>
      {drag && <div className="up-overlay" aria-hidden><div>Déposez vos fichiers 3D ici (STL, OBJ, 3MF, STEP)</div></div>}
      <Dialog open={open} onClose={close} title="Envoyer des fichiers" wide
        footer={<><Button onClick={close}>{busy ? 'Interrompre' : 'Fermer'}</Button>
          <Button variant="primary" icon={<Upload size={15} />} disabled={busy || pending === 0} onClick={start}>{busy ? 'Envoi en cours…' : `Envoyer ${pending} fichier(s)`}</Button></>}>
        <label className="up-drop">
          <Upload size={22} aria-hidden />
          <span>Glissez des fichiers ici, ou <u>parcourez</u></span>
          <span className="hint">STL, OBJ, 3MF, STEP{max ? ` · ${max} au plus par fichier` : ''}</span>
          <input type="file" multiple accept={ACCEPT} hidden disabled={busy} onChange={(e) => { add(Array.from(e.target.files ?? [])); e.target.value = ''; }} />
        </label>
        <div className="up-opts">
          <Field label="Collection (créée si besoin, « A/B » pour imbriquer)"><Input value={collection} onChange={(e) => setCollection(e.target.value)} disabled={busy} /></Field>
          <Field label="Étiquettes (virgules)"><Input value={tags} onChange={(e) => setTags(e.target.value)} disabled={busy} /></Field>
          <Field label="Licence"><Input value={license} onChange={(e) => setLicense(e.target.value)} disabled={busy} /></Field>
        </div>
        {items.length > 0 && (
          <ul className="up-list" aria-label="Fichiers à envoyer">
            {items.map((i) => (
              <li key={i.key} className={`up-item up-${i.state}`}>
                <span className="nm">{i.file.name}</span>
                <span className="sub mono">{fmtBytes(i.file.size)}</span>
                {(i.state === 'sending' || i.state === 'done') && (
                  <div className="up-bar"><ProgressBar label={`Envoi de ${i.file.name}`} source={{ state: i.state === 'done' ? 'succeeded' : 'running', done_bytes: i.sent, total_bytes: i.file.size, phase_label: i.state === 'done' ? 'Envoyé' : 'Envoi vers le serveur' }} /></div>
                )}
                {i.state === 'done' && i.result && (
                  <span className="up-res"><Tag tone="ok">{STATUS_LABEL[i.result.status] ?? i.result.status}</Tag>
                    {i.result.model_id && <> <a href={`/modele/${i.result.model_id}`}>Ouvrir la fiche</a></>}</span>
                )}
                {i.state === 'error' && <span className="up-err" role="alert">{i.error}</span>}
              </li>
            ))}
          </ul>
        )}
        {urlOn && (
          <fieldset className="up-url">
            <legend className="eyebrow">Ou depuis un lien</legend>
            <div className="up-url-row">
              <Input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://makerworld.com/… ou https://www.printables.com/model/…" disabled={busy} spellCheck={false} aria-label="Lien d'un modèle" />
              <Button icon={<Link2 size={15} />} disabled={busy || !url.trim()} onClick={doUrl}>Importer le lien</Button>
            </div>
            <p className="hint" style={{ margin: 0 }}>Domaines autorisés : {limits?.url_import.allowed_domains.join(', ')}. Si le site exige une connexion, téléchargez le fichier puis déposez-le ci-dessus.</p>
            {urlMsg && (urlMsg.tone === 'err' ? <Notice tone="err" title="Import par lien impossible">{urlMsg.text}</Notice> : <p role="status" style={{ margin: 0 }}>{urlMsg.text}</p>)}
          </fieldset>
        )}
        {limits && limits.import_roots.length > 0 && <p className="hint" style={{ margin: 0 }}>Les dossiers du serveur ({limits.import_roots.join(', ')}) s'importent avec « Importer ».</p>}
      </Dialog>
    </>
  );
}
