import { useEffect, useRef, useState } from 'react';
import { Download } from 'lucide-react';
import { Button, Field, Input, Toggle } from '../components';
import { describeError } from '../api/client';
import { exportApi } from '../api/exportApi';
import type { ExportPlan, ExportRecord, ExportStatus } from '../api/exportApi';
import { Dialog, Notice, fmtBytes } from './common';
import './export.css';

type Step = 'form' | 'running' | 'ready' | 'failed';

/**
 * « Exporter le projet » : archive zip partageable d'une collection (README en français, fichiers d'origine vérifiés,
 * profils de découpe, manifeste). Le travail est asynchrone ; le téléchargement passe par un lien signé à durée
 * limitée. Rien n'est publié : la liste « ce qu'il reste à faire à la main » est affichée avec le résultat.
 */
export function ExportDialog({ open, onClose, collection, collectionName }: { open: boolean; onClose: () => void; collection: string; collectionName: string }) {
  const [step, setStep] = useState<Step>('form');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const [title, setTitle] = useState('');
  const [description, setDescription] = useState('');
  const [license, setLicense] = useState('');
  const [assembly, setAssembly] = useState('');
  const [hardware, setHardware] = useState('');
  const [gcode, setGcode] = useState(false);
  const [photos, setPhotos] = useState(false);
  const [sub, setSub] = useState(true);
  const [plan, setPlan] = useState<ExportPlan>();
  const [status, setStatus] = useState<ExportStatus>();
  const ac = useRef<AbortController | null>(null);

  useEffect(() => {
    if (open) { setStep('form'); setError(undefined); setPlan(undefined); setStatus(undefined); }
    else ac.current?.abort();
  }, [open, collection]);
  useEffect(() => () => ac.current?.abort(), []);

  const start = async () => {
    setBusy(true); setError(undefined);
    ac.current?.abort(); ac.current = new AbortController();
    const signal = ac.current.signal;
    try {
      const st = await exportApi.start({
        collection_id: collection,
        options: {
          title: title.trim() || undefined, description: description.trim() || undefined, license: license.trim() || undefined,
          assembly_notes: assembly.trim() || undefined, hardware: hardware.trim() || undefined,
          include_gcode_3mf: gcode, include_photos: photos, skip_subcollections: !sub,
        },
      });
      setPlan(st.plan); setStep('running');
      for (;;) {
        const s = await exportApi.get(st.export_id, signal);
        setStatus(s);
        if (s.export.state !== 'queued') { setStep(s.export.state === 'ready' ? 'ready' : 'failed'); break; }
        await new Promise<void>((resolve, reject) => {
          const t = window.setTimeout(resolve, 600);
          signal.addEventListener('abort', () => { window.clearTimeout(t); reject(new DOMException('abort', 'AbortError')); }, { once: true });
        });
      }
    } catch (e) {
      if ((e as Error).name !== 'AbortError') { setError(describeError(e)); setStep((p) => (p === 'running' ? 'failed' : 'form')); }
    } finally { setBusy(false); }
  };

  const download = async (rec: ExportRecord) => {
    setBusy(true); setError(undefined);
    try {
      const l = await exportApi.link(rec.export_id);
      window.location.assign(exportApi.downloadUrl(l)); // lien signé : téléchargement direct, sans jeton
    } catch (e) { setError(describeError(e)); } finally { setBusy(false); }
  };

  const rec = status?.export;
  const warnings = [...(plan?.warnings ?? []), ...(rec?.summary?.warnings ?? [])];
  const skipped = [...(plan?.skipped ?? []), ...(rec?.summary?.skipped ?? [])];

  return (
    <Dialog open={open} onClose={onClose} wide title={`Exporter le projet « ${collectionName} »`}
      footer={step === 'form' ? (
        <><Button onClick={onClose}>Annuler</Button><Button variant="primary" disabled={busy} icon={<Download size={15} />} onClick={start}>Préparer l'archive</Button></>
      ) : step === 'ready' && rec ? (
        <><Button onClick={onClose}>Fermer</Button><Button variant="primary" disabled={busy} icon={<Download size={15} />} onClick={() => download(rec)}>Télécharger le zip ({fmtBytes(rec.size_bytes ?? 0)})</Button></>
      ) : <Button onClick={onClose}>{step === 'running' ? 'Continuer en arrière-plan' : 'Fermer'}</Button>}>
      {error && <Notice tone="err" title="Export impossible">{error}</Notice>}

      {step === 'form' && (
        <div className="exp-form">
          <p className="hint">Une archive à partager : fichiers d'origine (empreintes vérifiées), sources paramétriques, réglages de découpe modifiés, README en français et manifeste. Aucun secret, aucune donnée d'imprimante, aucun chemin du serveur. Rien n'est publié.</p>
          <Field label="Titre (défaut : nom de la collection)"><Input value={title} onChange={(e) => setTitle(e.target.value)} maxLength={120} placeholder={collectionName} /></Field>
          <Field label="Description"><textarea className="input" rows={2} value={description} onChange={(e) => setDescription(e.target.value)} maxLength={5000} /></Field>
          <Field label="Licence (défaut : non précisée)"><Input value={license} onChange={(e) => setLicense(e.target.value)} maxLength={200} placeholder="ex. CC BY-NC 4.0" /></Field>
          <Field label="Notice d'assemblage (texte libre)"><textarea className="input" rows={3} value={assembly} onChange={(e) => setAssembly(e.target.value)} maxLength={20000} /></Field>
          <Field label="Quincaillerie (une ligne par article)"><textarea className="input" rows={2} value={hardware} onChange={(e) => setHardware(e.target.value)} maxLength={10000} /></Field>
          <Toggle checked={sub} onChange={setSub} label="Inclure les sous-collections" />
          <Toggle checked={photos} onChange={setPhotos} label="Joindre les photos de fin d'impression du journal (EXIF retiré)" />
          <Toggle checked={gcode} onChange={setGcode} label="Joindre les .gcode.3mf validés" />
          {gcode && <Notice tone="warn" title="Fichiers propres à une imprimante">Un .gcode.3mf ne vaut que pour une imprimante, une buse et des réglages précis. Il est placé à part (printer-specific/) ; ne le publiez que si c'est voulu.</Notice>}
        </div>
      )}

      {step === 'running' && (
        <div className="exp-progress" role="status" aria-live="polite">
          <p>Préparation de l'archive{plan ? ` : ${plan.parts.length} pièce(s), ${plan.files} fichier(s), ${fmtBytes(plan.total_bytes)}` : ''}…</p>
          <progress value={Math.round((status?.job?.progress ?? 0) * 100)} max={100} />
          <p className="hint">{status?.job?.message ?? 'En file'}</p>
        </div>
      )}

      {step === 'failed' && rec && <Notice tone="err" title="L'export a échoué">{rec.error || 'Erreur inconnue.'}</Notice>}

      {(step === 'ready' || step === 'running') && (warnings.length > 0 || skipped.length > 0) && (
        <Notice tone="warn" title="À savoir">
          {warnings.concat(skipped.map((s) => `Laissé de côté : ${s.name ?? s.model_id} — ${s.reason}`)).map((w, i) => <span key={i} className="exp-line">{w}</span>)}
        </Notice>
      )}

      {step === 'ready' && rec && (
        <>
          <Notice tone="info" title="Archive prête">
            {rec.file_name} — {rec.summary?.parts ?? 0} pièce(s), {rec.summary?.files ?? 0} entrées. Conservée 24 h sur le serveur ; le lien de téléchargement est valable quelques minutes et se redemande ici.
          </Notice>
          <section aria-label="Ce qu'il reste à faire à la main" className="exp-steps">
            <h3>Pour publier (à faire à la main, rien n'est automatique)</h3>
            <ol>{(status?.publish_steps ?? []).map((s, i) => <li key={i}>{s}</li>)}</ol>
          </section>
        </>
      )}
    </Dialog>
  );
}
