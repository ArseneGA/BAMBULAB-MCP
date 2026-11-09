import { useEffect, useMemo, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import { Box, ExternalLink, Play, Plus, RefreshCw, ScanSearch, ShieldCheck } from 'lucide-react';
import { Button, EmptyState, Field, Input, Panel, ProgressBar, Select, Tabs, Tag, Toggle, useToast } from '../components';
import { api, assetUrl, describeError } from '../api/client';
import { useAsync, useConnection } from '../api/hooks';
import type { HistoryView, JobProgress, CancelOutput, ConfirmationView, ReconcileOutput, FilamentAssignment, PrintJob, PrintState, PrinterView, StartOutput, UploadOutput, ValidationReport } from '../api/printTypes';
import { printSource } from '../api/progress';
import { Async, Dialog, Notice, fmtDate } from './common';
import { CompletedBox } from './CompletedBox';
import { GcodePanel } from './GcodePanel';
import './print.css';

export const STATE_LABEL: Record<PrintState, { label: string; tone?: 'ok' | 'warn' | 'err' }> = {
  DRAFT: { label: 'brouillon' }, PREPARED: { label: 'préparée' }, SLICING: { label: 'découpe en cours', tone: 'warn' }, VALIDATED: { label: 'découpée' },
  UPLOADING: { label: 'envoi en cours', tone: 'warn' }, UPLOADED: { label: 'déposée sur l\'imprimante' }, AWAITING_PRINT_CONFIRMATION: { label: 'attend votre confirmation', tone: 'warn' },
  START_REQUESTED: { label: 'lancement demandé', tone: 'warn' }, PRINTING: { label: 'en cours d\'impression', tone: 'ok' }, COMPLETED: { label: 'terminée', tone: 'ok' },
  FAILED: { label: 'échec', tone: 'err' }, CANCELLED: { label: 'annulée' }, START_UNKNOWN: { label: 'lancement incertain', tone: 'err' },
};
const STEPS = ['Découpe', 'Validation', 'Envoi', 'Confirmation', 'Lancement'];
const stepIndex = (j: PrintJob): number => {
  if (['DRAFT', 'PREPARED', 'SLICING'].includes(j.state)) return 0;
  if (j.state === 'VALIDATED') return j.validation?.ok ? 2 : 1;
  if (j.state === 'UPLOADING') return 2;
  if (['UPLOADED', 'AWAITING_PRINT_CONFIRMATION'].includes(j.state)) return 3;
  return 4;
};
export const transient = (s: PrintState) => ['SLICING', 'UPLOADING', 'START_REQUESTED', 'PRINTING'].includes(s);
const uuid = () => (crypto.randomUUID ? crypto.randomUUID() : `k-${Date.now()}-${Math.random().toString(36).slice(2)}`);

const ACTIVE: PrintState[] = ['PREPARED', 'SLICING', 'UPLOADING', 'START_REQUESTED', 'PRINTING'];
/** Pastille d'état (maquette) : en cours = vert animé, attente = ambre, échec = rouge. */
export const pillClass = (s: PrintState): string => {
  if (s === 'PRINTING' || s === 'START_REQUESTED') return 'run';
  const t = STATE_LABEL[s]?.tone;
  return t === 'ok' ? 'ok' : t === 'warn' ? 'wait' : t === 'err' ? 'fail' : '';
};

export function PrintPage() {
  const [jid, setJid] = useState<string>('');
  const printers = useAsync((s) => api.printers(s), []);
  const [view, setView] = useState<HistoryView>('all');
  const [grouped, setGrouped] = useState(false);
  const jobs = useAsync((s) => api.printJobs(s, view, grouped), [view, grouped]);
  const reloadJobs = useRef(jobs.reload); reloadJobs.current = jobs.reload;
  const busy = jobs.data?.print_jobs.some((j) => transient(j.state) || j.state === 'PREPARED');
  useEffect(() => { if (!busy) return; const t = window.setInterval(() => reloadJobs.current(), 3000); return () => window.clearInterval(t); }, [busy]);

  const plist = printers.data?.printers ?? [];
  const job = jobs.data?.print_jobs.find((j) => j.id === jid);
  const toast = useToast();
  const prog = jobs.data?.progress ?? {};
  const counts = jobs.data?.counts ?? {};
  const history = async (action: 'archive' | 'trash' | 'restore', ids: string[]) => {
    try { const r = await api.printHistory(action, ids); toast('ok', r.note); jobs.reload(); } catch (e) { toast('err', describeError(e)); }
  };
  const failedIds = (jobs.data?.print_jobs ?? []).filter((j) => ['FAILED', 'CANCELLED'].includes(j.state)).map((j) => j.id);

  return (
    <>
      <div className="page-head">
        <div><h1>Impressions</h1><div className="sub">Découpe, validation, dépôt sur l'imprimante puis suivi. Le lancement exige votre accord sur la page de confirmation du serveur : rien ne démarre sans elle.</div></div>
        <div className="head-acts"><Button variant="secondary" icon={<RefreshCw size={14} />} onClick={jobs.reload}>Actualiser</Button></div>
      </div>
      <div className="prt">
        {printers.data && <NewJob printers={plist} defaultPrinter={plist[0]?.id} onCreated={(j) => { setJid(j.id); jobs.reload(); }} />}
        <Panel title="Historique" className="prt-hist flush">
          <div className="prt-hist-bar">
            <Tabs<HistoryView> label="Filtrer les impressions" value={view} onChange={setView} tabs={[
              { value: 'all', label: `Toutes (${counts.all ?? 0})` }, { value: 'active', label: `En cours (${counts.active ?? 0})` }, { value: 'finished', label: `Terminées (${counts.finished ?? 0})` },
              { value: 'failed', label: `Échecs (${counts.failed ?? 0})` }, { value: 'archived', label: `Archivées (${counts.archived ?? 0})` }, { value: 'trash', label: `Corbeille (${counts.trash ?? 0})` }]} />
            <Toggle checked={grouped} onChange={setGrouped} label="Grouper par modèle" />
            {view === 'failed' && failedIds.length > 0 && <Button size="sm" onClick={() => history('archive', failedIds)}>Archiver les {failedIds.length} échec(s)</Button>}
          </div>
          <Async state={jobs} label="Chargement des impressions" empty={(d) => d.print_jobs.length === 0 ? <p className="hint prt-empty">Aucune impression dans cette vue. Découpez un modèle pour une imprimante ci-dessus.</p> : null}>
            {(d) => grouped && d.groups ? (
              <>{d.groups.map((g) => (
                <details key={g.model_id || 'none'} open className="prt-group">
                  <summary><strong>{g.model_name}</strong> · {g.count} impression(s){g.failed > 0 && ` · ${g.failed} échec(s)`}</summary>
                  <JobRows jobs={g.jobs} jid={jid} setJid={setJid} prog={prog} history={history} printers={plist} />
                </details>))}</>
            ) : <JobRows jobs={d.print_jobs} jid={jid} setJid={setJid} prog={prog} history={history} printers={plist} />}
          </Async>
        </Panel>
        {job && <JobDetail key={job.id} job={job} printer={plist.find((p) => p.id === job.printer_id)} progress={prog[job.id]} onChange={jobs.reload} />}
      </div>
    </>
  );
}

export function stateText(j: PrintJob, p?: JobProgress): string {
  if (j.state === 'PREPARED' || j.state === 'SLICING') {
    if (p?.queue_position != null) return `en file (position ${p.queue_position})`;
    if (p?.stalled) return 'découpe bloquée ?';
    return p ? `découpe en cours · ${p.percent} %` : (STATE_LABEL[j.state]?.label ?? j.state);
  }
  if (j.state === 'UPLOADING' && p) return `envoi en cours · ${p.percent} %`;
  if (j.state === 'PRINTING' && p) return `en cours d'impression · ${p.percent} %`;
  if (j.state === 'CANCELLED' && j.stopped_by_user) return 'arrêtée par vous';
  return STATE_LABEL[j.state]?.label ?? j.state;
}

/** « Lancer » ne lance rien ici : il mène à la page de confirmation du serveur (/confirm/…), où l'humain approuve. */
export function LaunchButton({ job, size = 'sm', onNeedConfirm }: { job: PrintJob; size?: 'sm' | 'md'; onNeedConfirm?: () => void }) {
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  if (!['UPLOADED', 'AWAITING_PRINT_CONFIRMATION'].includes(job.state)) return null;
  const go = async () => {
    if (job.confirmation_id) { window.location.assign(`/confirm/${encodeURIComponent(job.confirmation_id)}`); return; }
    setBusy(true);
    try { const c = await api.requestConfirmation(job.id); window.location.assign(assetUrl(c.confirm_url)); }
    catch (e) { toast('err', describeError(e)); onNeedConfirm?.(); } finally { setBusy(false); }
  };
  return <Button variant="primary" size={size} icon={<Play size={13} />} disabled={busy} onClick={go} title="Ouvre la page de confirmation : vous y approuvez le lancement">{busy ? 'Demande…' : 'Lancer'}</Button>;
}

export function JobRows({ jobs, jid, setJid, prog, history, printers }: { jobs: PrintJob[]; jid: string; setJid: (id: string) => void; prog: Record<string, JobProgress>; history: (a: 'archive' | 'trash' | 'restore', ids: string[]) => void; printers: PrinterView[] }) {
  return (
    <ul className="jobs" aria-label="Impressions">
      {jobs.map((j) => {
        const p = prog[j.id];
        const active = ACTIVE.includes(j.state);
        const pr = printers.find((x) => x.id === j.printer_id);
        return (
          <li key={j.id} className={`job${jid === j.id ? ' sel' : ''}`}>
            <span className="job-th" aria-hidden><Box size={22} /></span>
            <button type="button" className="job-main" aria-pressed={jid === j.id} onClick={() => setJid(jid === j.id ? '' : j.id)}>
              <b>{j.name ?? j.id}</b>
              <span className="job-note">{j.last_error ? j.last_error : `${pr?.name ?? j.printer_id}${j.plate ? ` · plaque ${j.plate}` : ''} · ${fmtDate(j.updated_at)}`}</span>
            </button>
            <span className={`pill ${pillClass(j.state)}`}><i />{stateText(j, p)}</span>
            <span className="job-acts">
              <LaunchButton job={j} />
              {['COMPLETED', 'FAILED', 'CANCELLED'].includes(j.state) && (j.archived || j.trashed
                ? <Button size="sm" variant="ghost" onClick={() => history('restore', [j.id])}>Restaurer</Button>
                : <><Button size="sm" variant="ghost" onClick={() => history('archive', [j.id])}>Archiver</Button>
                  <Button size="sm" variant="ghost" onClick={() => history('trash', [j.id])}>Corbeille</Button></>)}
            </span>
            {active && <div className="job-bar"><ProgressBar label={`Avancement : ${j.name ?? j.id}`} source={printSource(j, p)} /></div>}
          </li>
        );
      })}
    </ul>
  );
}

/** Lien utile quand aucune imprimante n'est enregistrée. */
export function NoPrinterHint() {
  return <EmptyState title="Aucune imprimante enregistrée" text="Ajoutez votre imprimante (modèle, adresse sur le LAN, numéro de série) ; le code d'accès se saisit ensuite sur une page locale, jamais ici." action={<Link to="/imprimantes"><Button size="sm" variant="primary" icon={<Plus size={13} />}>Ajouter une imprimante</Button></Link>} />;
}

export function AddPrinter({ open, onClose, onAdded }: { open: boolean; onClose: () => void; onAdded: (p: PrinterView) => void }) {
  const models = useAsync((s) => api.printerModels(s), []);
  const [f, setF] = useState({ name: '', model_id: '', host: '', serial: '', nozzle: '0.4', ams: '0' });
  const [err, setErr] = useState<string>();
  const [busy, setBusy] = useState(false);
  const ok = f.name.trim() && f.model_id && f.host.trim() && f.serial.trim();
  const submit = async () => {
    setBusy(true); setErr(undefined);
    try { onAdded(await api.addPrinter({ name: f.name.trim(), model_id: f.model_id, host: f.host.trim(), serial: f.serial.trim(), nozzle_mm: Number(f.nozzle) || undefined, ams_units: Number(f.ams) || 0 })); }
    catch (e) { setErr(describeError(e)); } finally { setBusy(false); }
  };
  return (
    <Dialog open={open} onClose={onClose} title="Ajouter une imprimante" footer={<><Button onClick={onClose}>Annuler</Button><Button variant="primary" disabled={!ok || busy} onClick={submit}>{busy ? 'Ajout…' : 'Ajouter'}</Button></>}>
      {err && <Notice tone="err" title="Ajout refusé">{err}</Notice>}
      <Field label="Nom d'usage"><Input value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} placeholder="Atelier" /></Field>
      <Field label="Modèle"><Select value={f.model_id} onChange={(e) => setF({ ...f, model_id: e.target.value })} options={[{ value: '', label: 'Choisir…' }, ...(models.data?.models ?? []).map((m) => ({ value: m.id, label: m.name }))]} /></Field>
      <Field label="Adresse sur le réseau local (IP ou nom)"><Input value={f.host} onChange={(e) => setF({ ...f, host: e.target.value })} spellCheck={false} /></Field>
      <Field label="Numéro de série"><Input value={f.serial} onChange={(e) => setF({ ...f, serial: e.target.value })} spellCheck={false} /></Field>
      <Field label="Buse installée (mm)"><Input value={f.nozzle} onChange={(e) => setF({ ...f, nozzle: e.target.value })} inputMode="decimal" /></Field>
      <Field label="Nombre d'AMS raccordés"><Select value={f.ams} onChange={(e) => setF({ ...f, ams: e.target.value })} options={[0, 1, 2, 3, 4].map((n) => ({ value: String(n), label: String(n) }))} /></Field>
      <p className="hint" style={{ margin: 0 }}>L'adresse doit être privée (192.168.x.x, 10.x, 172.16-31.x, lien local ou nom .local). Le certificat de l'imprimante est épinglé par le serveur sur la page locale de saisie, où vous confirmez son empreinte avec le code d'accès LAN, qui n'est pas demandé ici : après l'ajout, une page locale du serveur le recueille.</p>
    </Dialog>
  );
}

export function NewJob({ printers, defaultPrinter, onCreated }: { printers: PrinterView[]; defaultPrinter?: string; onCreated: (j: PrintJob) => void }) {
  const toast = useToast();
  const models = useAsync((s) => api.models({ limit: 100, sort: 'name' }, s), []);
  const [model, setModel] = useState('');
  const [printer, setPrinter] = useState('');
  const [busy, setBusy] = useState(false);
  const pr = printer || defaultPrinter || '';
  const go = async () => {
    setBusy(true);
    try { const j = await api.createPrintJob({ model_id: model, printer_id: pr, plate: 1, idempotency_key: uuid() }); toast('ok', 'Découpe lancée.'); onCreated(j); }
    catch (e) { toast('err', describeError(e)); } finally { setBusy(false); }
  };
  return (
    <Panel title="Nouvelle impression">
      {printers.length === 0 ? <NoPrinterHint /> : <div className="prt-new">
        <Field label="Modèle (dernière version, profil appliqué le cas échéant)"><Select value={model} onChange={(e) => setModel(e.target.value)} options={[{ value: '', label: 'Choisir…' }, ...(models.data?.models ?? []).filter((m) => m.formats.some((f) => ['stl', 'obj', '3mf'].includes(f))).map((m) => ({ value: m.id, label: m.name }))]} /></Field>
        <Field label="Imprimante"><Select value={pr} onChange={(e) => setPrinter(e.target.value)} options={[{ value: '', label: 'Choisir…' }, ...printers.map((p) => ({ value: p.id, label: `${p.name} (${p.model_name})` }))]} /></Field>
        <Button variant="primary" disabled={!model || !pr || busy} onClick={go}>{busy ? 'Création…' : 'Découper pour cette imprimante'}</Button>
      </div>}
    </Panel>
  );
}

function trays(ams: number): string[] { return ['ext', ...Array.from({ length: ams * 4 }, (_, i) => `ams${Math.floor(i / 4) + 1}:${(i % 4) + 1}`)]; }

function JobDetail({ job, printer, progress, onChange }: { job: PrintJob; printer?: PrinterView; progress?: JobProgress; onChange: () => void }) {
  const toast = useToast();
  const step = stepIndex(job);
  const filaments = job.summary?.filament_presets ?? [];
  const [assign, setAssign] = useState<FilamentAssignment[]>(() => (job.assignments?.length ? job.assignments : (filaments.length ? filaments : ['']).map((_, i) => ({ filament_slot: i + 1, tray: 'ext', material_id: '' }))));
  const [report, setReport] = useState<ValidationReport | undefined>(job.validation);
  const [upload, setUpload] = useState<UploadOutput>();
  const [conf, setConf] = useState<ConfirmationView>();
  const [start, setStart] = useState<StartOutput>();
  const [stop, setStop] = useState<CancelOutput>();
  const [recon, setRecon] = useState<ReconcileOutput>();
  const [busy, setBusy] = useState<string | null>(null);
  const upKey = useRef(uuid()), startKey = useRef(uuid());
  const complete = assign.every((a) => a.material_id.trim() !== '');
  const wrap = async (name: string, fn: () => Promise<void>) => { setBusy(name); try { await fn(); } catch (e) { toast('err', describeError(e)); } finally { setBusy(null); } };

  // Confirmation déjà demandée : relecture régulière jusqu'à l'approbation humaine.
  const cid = conf?.confirmation_id ?? job.confirmation_id;
  useEffect(() => {
    if (!cid || start || !['UPLOADED', 'AWAITING_PRINT_CONFIRMATION'].includes(job.state)) return;
    let alive = true;
    const tick = () => api.confirmation(cid).then((c) => alive && setConf(c), () => undefined);
    void tick(); const t = window.setInterval(tick, 2500);
    return () => { alive = false; window.clearInterval(t); };
  }, [cid, job.state, start]);

  const live = useAsync((s) => api.printStatus(job.id, s), [job.id, job.state]);
  const liveReload = useRef(live.reload); liveReload.current = live.reload;
  useEffect(() => { if (!['PRINTING', 'START_REQUESTED'].includes(job.state)) return; const t = window.setInterval(() => liveReload.current(), 4000); return () => window.clearInterval(t); }, [job.state]);
  const pg = live.data?.printer;
  const trayOpts = useMemo(() => trays(printer?.ams_units ?? 0).map((t) => ({ value: t, label: t === 'ext' ? 'bobine externe' : t.replace('ams', 'AMS ').replace(':', ' · bobine ') })), [printer?.ams_units]);
  const setA = (i: number, p: Partial<FilamentAssignment>) => setAssign((l) => l.map((a, k) => (k === i ? { ...a, ...p } : a)));

  return (
    <Panel title={`Impression : ${job.name ?? job.id}`} actions={<Tag tone={STATE_LABEL[job.state]?.tone}>{STATE_LABEL[job.state]?.label ?? job.state}</Tag>}>
      <div className="prt-detail">
        <ol className="prt-steps" aria-label="Étapes">{STEPS.map((s, i) => <li key={s} aria-current={i === step ? 'step' : undefined} data-done={i < step}>{s}</li>)}</ol>
        <p className="hint mono" style={{ margin: 0 }}>{job.id} · {printer?.name ?? job.printer_id} · plaque {job.plate ?? 1}{job.artifact_sha256 && ` · sha256 ${job.artifact_sha256.slice(0, 12)}…`}</p>
        {job.last_error && <Notice tone="err" title="Dernière erreur">{job.last_error}</Notice>}
        {job.artifact_sha256 && !['DRAFT', 'PREPARED', 'SLICING', 'FAILED', 'CANCELLED'].includes(job.state) && <GcodePanel printJobId={job.id} plate={job.plate} />}

        {(job.state === 'SLICING' || job.state === 'PREPARED') && <div role="status"><ProgressBar large label="Avancement de la découpe" source={printSource(job, progress)}><p className="hint">Bambu Studio découpe le modèle (rien n'est envoyé à l'imprimante).</p></ProgressBar></div>}

        {job.state === 'VALIDATED' && (
          <>
            <fieldset className="prt-fs"><legend className="eyebrow">Matières chargées</legend>
              {assign.map((a, i) => (
                <div key={i} className="prt-assign">
                  <span className="mono">filament {a.filament_slot}{filaments[i] ? ` : ${filaments[i]}` : ''}</span>
                  <Select aria-label={`Emplacement du filament ${a.filament_slot}`} value={a.tray} onChange={(e) => setA(i, { tray: e.target.value })} options={trayOpts} />
                  <Input aria-label={`Matière du filament ${a.filament_slot}`} placeholder="Matière réellement chargée (ex. PLA Basic gris)" value={a.material_id} onChange={(e) => setA(i, { material_id: e.target.value })} />
                </div>
              ))}
            </fieldset>
            <div className="prt-acts">
              <Button icon={<ShieldCheck size={14} />} disabled={!complete || busy !== null} onClick={() => wrap('validate', async () => { const r = await api.validatePrint({ print_job_id: job.id, printer_id: job.printer_id, assignments: assign }); setReport(r.report); onChange(); })}>{busy === 'validate' ? 'Validation…' : "Valider pour l'imprimante"}</Button>
              <Button variant="primary" disabled={!report?.ok || busy !== null} onClick={() => wrap('upload', async () => { setUpload(await api.uploadPrint({ print_job_id: job.id, printer_id: job.printer_id, idempotency_key: upKey.current, assignments: assign })); onChange(); })}>{busy === 'upload' ? 'Envoi…' : "Envoyer sur l'imprimante"}</Button>
            </div>
            {!complete && <p className="hint" style={{ margin: 0 }}>Indiquez la matière de chaque filament : la liaison filament, emplacement et matière fait partie de ce que vous approuverez.</p>}
            {report && <Report r={report} />}
          </>
        )}
        {job.state === 'UPLOADING' && <div role="status"><ProgressBar large label="Avancement du dépôt sur l'imprimante" source={printSource(job, progress)}><p className="hint">Transfert du fichier vers l'imprimante (rien n'est lancé).</p></ProgressBar></div>}
        {upload?.question && <Notice title="À vérifier avant de continuer">{upload.question}</Notice>}

        {['UPLOADED', 'AWAITING_PRINT_CONFIRMATION'].includes(job.state) && !start && (
          <>
            {!conf && !job.confirmation_id && (
              <div className="prt-acts">
                <Button variant="primary" disabled={busy !== null} onClick={() => wrap('conf', async () => { setConf(await api.requestConfirmation(job.id)); onChange(); })}>{busy === 'conf' ? 'Demande…' : 'Demander la confirmation de lancement'}</Button>
                <span className="hint">Demander n'est pas lancer : la page de confirmation vous montre ce qui sera imprimé.</span>
              </div>
            )}
            {conf && <ConfirmBox c={conf} onStart={() => wrap('start', async () => { const o = await api.startPrint({ print_job_id: job.id, confirmation_id: conf.confirmation_id, idempotency_key: startKey.current }); setStart(o); onChange(); })} starting={busy === 'start'} />}
          </>
        )}
        {start && <Notice tone={start.outcome === 'start_unknown' || start.outcome === 'rejected' ? 'err' : 'info'} title={`Lancement : ${start.outcome}`}>{start.note}</Notice>}

        {['START_REQUESTED', 'PRINTING'].includes(job.state) && (
          <div role="status">
            <ProgressBar large label="Avancement de l'impression" source={printSource(job, progress, pg)} />
            {live.data?.note && <p className="hint">{live.data.note}</p>}
          </div>
        )}
        {job.state === 'PRINTING' && (
          <div className="prt-acts">
            <Button variant="danger" disabled={busy !== null} onClick={() => wrap('stop', async () => setStop(await api.cancelPrint(job.id)))}>{busy === 'stop' ? 'Demande…' : "Demander l'arrêt…"}</Button>
            <span className="hint">Cela ne stoppe rien : l'arrêt se valide sur une page dédiée.</span>
          </div>
        )}
        {stop && (
          <Notice tone={stop.outcome === 'refused' ? 'err' : 'info'} title={`Arrêt : ${stop.outcome}`} actions={stop.stop_url ? <a className="btn btn-danger btn-sm" href={assetUrl(stop.stop_url)} target="_blank" rel="noreferrer"><ExternalLink size={13} /> Ouvrir la page d'arrêt</a> : undefined}>{stop.note}</Notice>
        )}
        {job.state === 'START_UNKNOWN' && (
          <Notice tone="err" title="Lancement incertain : ne pas relancer" actions={<Button icon={<ScanSearch size={14} />} disabled={busy !== null} onClick={() => wrap('recon', async () => { setRecon(await api.reconcilePrint(job.id)); onChange(); })}>{busy === 'recon' ? "Lecture de l'imprimante…" : "Vérifier l'état réel"}</Button>}>
            Le service ne sait pas si l'imprimante a démarré. Vérifiez sur la machine, puis lisez son état réel : la vérification est en lecture seule et n'envoie jamais de commande de lancement. Un nouveau lancement exigerait une nouvelle confirmation sur la page serveur.
          </Notice>
        )}
        {recon && <Notice tone={recon.outcome === 'unknown' ? 'warn' : 'info'} title={`Réconciliation : ${{ started: "l'impression tourne", not_started: "rien n'a démarré", unknown: 'impossible de conclure' }[recon.outcome]}`}>{recon.reason}. {recon.note}</Notice>}
        {job.state === 'COMPLETED' && <CompletedBox job={job} />}
        {job.next && !['VALIDATED', 'UPLOADED', 'AWAITING_PRINT_CONFIRMATION'].includes(job.state) && !['COMPLETED', 'FAILED', 'CANCELLED'].includes(job.state) && <p className="hint" style={{ margin: 0 }}>Étape suivante : {job.next}</p>}
        {job.events && job.events.length > 0 && <details><summary>Historique ({job.events.length})</summary><ul className="audit">{job.events.slice().reverse().slice(0, 10).map((e, i) => <li key={i}><span className="mono">{fmtDate(e.at)}</span><span>{e.from} → {e.to}</span><span className="sub">{e.note ?? e.event}</span></li>)}</ul></details>}
      </div>
    </Panel>
  );
}

function Report({ r }: { r: ValidationReport }) {
  const all = [...r.archive_checks, ...r.compatibility_checks];
  return (
    <div>
      <p style={{ margin: '0 0 var(--sp-2)' }}><Tag tone={r.ok ? 'ok' : 'err'}>{r.ok ? 'validé' : 'refusé'}</Tag> <span className="hint">{r.validator}</span></p>
      {r.blocking && r.blocking.length > 0 && <Notice tone="err" title="Contrôles bloquants">{r.blocking.join(' ; ')}</Notice>}
      <ul className="prt-checks">{all.map((c, i) => <li key={i}><Tag tone={c.ok ? 'ok' : c.level === 'error' ? 'err' : 'warn'}>{c.ok ? 'ok' : c.level === 'error' ? 'bloquant' : 'avis'}</Tag> {c.name}{c.detail && <span className="hint"> — {c.detail}</span>}</li>)}</ul>
      {r.warnings?.map((w) => <p key={w} className="hint" style={{ margin: 0 }}>{w}</p>)}
    </div>
  );
}

/**
 * Le seul chemin vers « Lancer » : la page de confirmation du serveur. Le bouton de lancement ci-dessous
 * n'apparaît actif qu'APRÈS l'approbation humaine enregistrée par cette page (confirmation.approved) ;
 * le serveur le refuse de toute façon sans confirmation approuvée, liée à cette impression, à usage unique.
 */
function ConfirmBox({ c, onStart, starting }: { c: ConfirmationView; onStart: () => void; starting: boolean }) {
  const b = c.binding;
  const conn = useConnection();
  return (
    <div className="prt-confirm">
      <h3>Confirmation de lancement</h3>
      <dl className="kvl">
        <dt>Fichier</dt><dd className="mono">{b.file}</dd>
        <dt>Imprimante</dt><dd>{b.printer_name} ({b.printer_model})</dd>
        <dt>Empreinte</dt><dd className="mono">{b.sha256.slice(0, 16)}…</dd>
        <dt>Plaque</dt><dd className="mono">{b.plate}</dd>
        <dt>Matières</dt><dd>{b.materials.map((m) => `f${m.filament_slot} → ${m.tray} : ${m.material_id}`).join(' ; ')}</dd>
        <dt>Valable jusqu'à</dt><dd className="mono">{fmtDate(c.expires_at)}</dd>
      </dl>
      {!c.approved ? (
        <>
          <p style={{ margin: 0 }}>{c.instructions}</p>
          <a className="btn btn-primary" href={assetUrl(c.confirm_url)} target="_blank" rel="noreferrer"><ExternalLink size={14} /> Ouvrir la page de confirmation</a>
          {conn.browserEnrolled === false && <Notice title="Ce navigateur n'est pas enregistré" actions={<a className="btn btn-secondary btn-sm" href="/enroll" target="_blank" rel="noreferrer">Enregistrer ce navigateur</a>}>La page de confirmation exige un navigateur enregistré : lancez <span className="mono">bambud enroll</span> sur la machine du service, puis saisissez le code sur /enroll.</Notice>}
          <p className="hint" style={{ margin: 0 }} role="status"><span className="spinner" aria-hidden /> En attente de votre accord sur cette page (vérification toutes les 2,5 s)…</p>
        </>
      ) : (
        <>
          <Notice tone="info" icon={<ShieldCheck size={16} aria-hidden />} title="Accord enregistré par vous, pour cette impression seulement">Vous pouvez maintenant lancer ; l'autorisation est à usage unique.</Notice>
          <Button variant="primary" disabled={starting} onClick={onStart}>{starting ? 'Lancement…' : "Lancer l'impression (autorisée)"}</Button>
        </>
      )}
    </div>
  );
}
