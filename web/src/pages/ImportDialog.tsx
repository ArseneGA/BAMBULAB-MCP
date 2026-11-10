import { useEffect, useRef, useState } from 'react';
import { FolderSearch, Upload } from 'lucide-react';
import { Button, Field, Input, ProgressBar, Segmented, Tag, Toggle } from '../components';
import { api, describeError, waitForJob } from '../api/client';
import { useConnection } from '../api/hooks';
import type { ImportReport, Job, ScanReport } from '../api/types';
import { Dialog, Notice, fmtBytes } from './common';

const COUNT_LABELS: Record<string, string> = {
  imported: 'importés', new_version: 'nouvelles versions', duplicate: 'doublons ignorés', unchanged: 'inchangés', indexed: 'indexés (non copiés)',
  upgraded: 'copiés (étaient Windows seul)', rejected: 'rejetés', error: 'en erreur',
};

type Step = 'path' | 'scanned' | 'running' | 'done' | 'failed';

/**
 * Import non destructif en deux temps : inventaire en lecture seule (POST /api/scan),
 * puis import (POST /api/import -> travail suivi par GET /api/jobs/{id}).
 * Les originaux ne sont jamais déplacés ni modifiés ; les chemins doivent être sous une racine autorisée par le service.
 */
export function ImportDialog({ open, onClose, onImported }: { open: boolean; onClose: () => void; onImported: () => void }) {
  const conn = useConnection();
  const [path, setPath] = useState('');
  const [step, setStep] = useState<Step>('path');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const [scan, setScan] = useState<ScanReport>();
  const [collection, setCollection] = useState('');
  const [copy, setCopy] = useState(true);
  const [dup, setDup] = useState<'skip' | 'keep'>('skip');
  const [tags, setTags] = useState('');
  const [license, setLicense] = useState('');
  const [job, setJob] = useState<Job>();
  const ac = useRef<AbortController | null>(null);

  useEffect(() => {
    if (open) { setStep('path'); setError(undefined); setScan(undefined); setJob(undefined); }
    else ac.current?.abort();
  }, [open]);
  useEffect(() => () => ac.current?.abort(), []);

  const doScan = async () => {
    setBusy(true); setError(undefined);
    try { setScan(await api.scan(path.trim())); setStep('scanned'); } catch (e) { setError(describeError(e)); } finally { setBusy(false); }
  };

  const doImport = async () => {
    setBusy(true); setError(undefined);
    ac.current?.abort(); ac.current = new AbortController();
    try {
      const started = await api.import({
        paths: [path.trim()], collection: collection.trim() || undefined, copy, on_duplicate: dup,
        tags: tags.split(',').map((t) => t.trim()).filter(Boolean), license: license.trim() || undefined,
      });
      setStep('running');
      const final = await waitForJob(started.job_id, setJob, ac.current.signal);
      setJob(final);
      setStep(final.state === 'succeeded' ? 'done' : 'failed');
      if (final.state === 'succeeded') onImported();
    } catch (e) {
      if ((e as Error).name !== 'AbortError') { setError(describeError(e)); setStep(job ? 'failed' : 'scanned'); }
    } finally { setBusy(false); }
  };

  const cancel = async () => { if (job) { try { await api.cancelJob(job.id); } catch (e) { setError(describeError(e)); } } };

  const report = job?.state === 'succeeded' ? (job.result as ImportReport | undefined) : undefined;
  const disabledReason = conn.server === 'offline' ? 'Printalk est injoignable.' : !conn.importEnabled ? conn.importNote ?? "Aucune racine d'import n'est autorisée." : undefined;

  const footer = (() => {
    switch (step) {
      case 'path': return <><Button onClick={onClose}>Fermer</Button><Button variant="primary" icon={<FolderSearch size={15} />} disabled={!path.trim() || busy || !!disabledReason} onClick={doScan}>Inventorier (lecture seule)</Button></>;
      case 'scanned': return <><Button onClick={() => setStep('path')}>Changer de dossier</Button>
        <Button variant="primary" icon={<Upload size={15} />} disabled={busy || scan?.total_files === 0 || scan?.enough_space === false} onClick={doImport}>
          Importer {scan ? scan.new_files + scan.changed_files : 0} fichier(s)
        </Button></>;
      case 'running': return <Button onClick={cancel} disabled={job?.cancel_requested}>{job?.cancel_requested ? 'Annulation demandée…' : "Annuler l'import"}</Button>;
      default: return <Button variant="primary" onClick={onClose}>Fermer</Button>;
    }
  })();

  return (
    <Dialog open={open} onClose={onClose} title="Importer des modèles" wide footer={footer}>
      {disabledReason && <Notice title="Import indisponible">{disabledReason} Autorisez un dossier d'import au démarrage du service : option <span className="mono">--import-root &lt;dossier&gt;</span> (ou variable <span className="mono">BAMBU_IMPORT_ROOTS</span>).</Notice>}
      {error && <Notice tone="err" title="Erreur">{error}</Notice>}

      {(step === 'path' || step === 'scanned') && (
        <Field label="Dossier ou fichier à importer (chemin absolu sur le serveur)">
          <Input value={path} onChange={(e) => { setPath(e.target.value); if (step === 'scanned') setStep('path'); }} placeholder="C:\Modeles\Atelier" spellCheck={false} disabled={busy} onKeyDown={(e) => { if (e.key === 'Enter' && path.trim() && !disabledReason) void doScan(); }} />
        </Field>
      )}
      {step === 'path' && <p className="hint" style={{ margin: 0 }}>Le dossier doit se trouver sous une racine d'import autorisée par le service. L'inventaire ne copie ni ne modifie rien ; les originaux ne sont jamais déplacés.</p>}

      {step === 'scanned' && scan && (
        <>
          <div className="kv" role="group" aria-label="Inventaire">
            <div><span className="k">Fichiers 3D</span><span className="v">{scan.total_files}</span></div>
            <div><span className="k">Nouveaux</span><span className="v">{scan.new_files}</span></div>
            <div><span className="k">Modifiés</span><span className="v">{scan.changed_files}</span></div>
            <div><span className="k">Déjà importés</span><span className="v">{scan.already_imported}</span></div>
            <div><span className="k">À copier</span><span className="v">{fmtBytes(scan.bytes_to_copy)}</span></div>
            {scan.free_disk_bytes !== undefined && <div><span className="k">Espace libre</span><span className="v">{fmtBytes(scan.free_disk_bytes)}</span></div>}
          </div>
          <p style={{ margin: 0, display: 'flex', gap: 4, flexWrap: 'wrap', alignItems: 'center' }}>
            {Object.entries(scan.by_format).map(([f, s]) => <Tag key={f}>{f.toUpperCase()} ×{s.count}</Tag>)}
            {scan.unsupported_files > 0 && <Tag tone="warn">{scan.unsupported_files} non pris en charge</Tag>}
          </p>
          {scan.enough_space === false && <Notice tone="err" title="Espace disque insuffisant">Le volume de stockage ne peut pas accueillir la copie : libérez de la place ou importez sans copie.</Notice>}
          {scan.total_files === 0 && <Notice title="Aucun fichier 3D">Formats pris en charge : STL, OBJ, 3MF, STEP.</Notice>}
          {scan.notes?.map((n) => <p key={n} className="hint" style={{ margin: 0 }}>{n}</p>)}
          {scan.walk_truncated && <Notice title="Inventaire partiel">Le dossier contient plus d'entrées que la limite du parcours.</Notice>}

          <fieldset style={{ border: '1px solid var(--line)', borderRadius: 'var(--r-2)', padding: 'var(--sp-3)', display: 'grid', gap: 'var(--sp-3)' }}>
            <legend className="eyebrow" style={{ padding: '0 6px' }}>Options</legend>
            <Field label="Collection de destination (vide = nom du dossier)"><Input value={collection} onChange={(e) => setCollection(e.target.value)} /></Field>
            <Field label="Étiquettes (séparées par des virgules)"><Input value={tags} onChange={(e) => setTags(e.target.value)} /></Field>
            <Field label="Licence (vide = inconnue)"><Input value={license} onChange={(e) => setLicense(e.target.value)} placeholder="CC0-1.0, usage personnel…" /></Field>
            <Toggle checked={copy} onChange={setCopy} label="Copier dans le stockage du service (sinon indexation seule : fichiers Windows uniquement)" />
            <Segmented<'skip' | 'keep'> label="Doublons exacts" value={dup} onChange={setDup} options={[{ value: 'skip', label: 'Ignorer les doublons' }, { value: 'keep', label: 'Les garder' }]} />
          </fieldset>
        </>
      )}

      {(step === 'running' || step === 'done' || step === 'failed') && job && (
        <>
          <ProgressBar source={job} label="Avancement de l'import" large />
          <p style={{ margin: 0 }} role="status">
            <Tag tone={job.state === 'succeeded' ? 'ok' : job.state === 'failed' ? 'err' : undefined}>{stateLabel(job.state)}</Tag>{' '}
            <span className="mono">{job.id}</span> {job.message && <>· {job.message}</>}
          </p>
          {job.error && <Notice tone="err" title="L'import a échoué">{job.error.message} <span className="mono">({job.error.code})</span></Notice>}
          {report && (
            <>
              <div className="kv" role="group" aria-label="Résultat">
                {Object.entries(report.counts).filter(([, n]) => n > 0).map(([k, n]) => <div key={k}><span className="k">{COUNT_LABELS[k] ?? k}</span><span className="v">{n}</span></div>)}
                <div><span className="k">Copiés</span><span className="v">{fmtBytes(report.bytes_copied)}</span></div>
              </div>
              <p className="hint" style={{ margin: 0 }}>L'analyse géométrique et les aperçus 3D sont calculés ensuite en arrière-plan : les fiches se complètent en quelques secondes.</p>
            </>
          )}
          {job.logs && job.logs.length > 0 && <pre className="logbox" aria-label="Journal du travail">{job.logs.slice(-12).join('\n')}</pre>}
        </>
      )}
    </Dialog>
  );
}

const stateLabel = (s: string) => ({ queued: 'En file', running: 'En cours', succeeded: 'Terminé', failed: 'Échec', cancelled: 'Annulé', unknown: 'Issue inconnue' } as Record<string, string>)[s] ?? s;
