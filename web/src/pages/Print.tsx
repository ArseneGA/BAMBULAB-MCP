import { useEffect, useMemo, useRef, useState } from 'react';
import { ExternalLink, Plus, RefreshCw, ScanSearch, ShieldCheck } from 'lucide-react';
import { Button, EmptyState, Field, Input, List, Panel, Select, StatusDot, Tag, useToast } from '../components';
import { api, assetUrl, describeError } from '../api/client';
import { useAsync, useConnection } from '../api/hooks';
import type { CancelOutput, ConfirmationView, ReconcileOutput, FilamentAssignment, PrintJob, PrintState, PrinterView, StartOutput, UploadOutput, ValidationReport } from '../api/printTypes';
import { Async, Dialog, Notice, fmtDate } from './common';
import { CompletedBox } from './CompletedBox';
import { CameraPanel } from './CameraPanel';
import './print.css';

const STATE_LABEL: Record<PrintState, { label: string; tone?: 'ok' | 'warn' | 'err' }> = {
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
const transient = (s: PrintState) => ['SLICING', 'UPLOADING', 'START_REQUESTED', 'PRINTING'].includes(s);
const uuid = () => (crypto.randomUUID ? crypto.randomUUID() : `k-${Date.now()}-${Math.random().toString(36).slice(2)}`);

export function PrintPage() {
  const [pid, setPid] = useState<string>('');
  const [jid, setJid] = useState<string>('');
  const printers = useAsync((s) => api.printers(s), []);
  const jobs = useAsync((s) => api.printJobs(s), []);
  const reloadJobs = useRef(jobs.reload); reloadJobs.current = jobs.reload;
  const busy = jobs.data?.print_jobs.some((j) => transient(j.state));
  useEffect(() => { if (!busy) return; const t = window.setInterval(() => reloadJobs.current(), 3000); return () => window.clearInterval(t); }, [busy]);
  const [addOpen, setAddOpen] = useState(false);

  const plist = printers.data?.printers ?? [];
  const printer = plist.find((p) => p.id === pid) ?? plist[0];
  const job = jobs.data?.print_jobs.find((j) => j.id === jid);

  return (
    <>
      <div className="page-head"><span className="eyebrow">Espace</span><h1>Impression</h1><p>Registre des imprimantes, découpe, validation, dépôt du fichier puis demande de confirmation. Le lancement exige votre accord sur la page de confirmation du serveur : rien ne démarre sans elle.</p></div>
      <div className="prt">
        <div className="prt-col">
          <Panel title="Imprimantes" actions={<Button size="sm" icon={<Plus size={13} />} onClick={() => setAddOpen(true)}>Ajouter</Button>}>
            <Async state={printers} label="Chargement du registre" empty={(d) => d.printers.length === 0 ? <EmptyState title="Aucune imprimante enregistrée" text="Ajoutez votre imprimante (modèle, adresse sur le LAN, numéro de série) ; le code d'accès se saisit ensuite sur une page locale, jamais ici." action={<Button size="sm" variant="primary" icon={<Plus size={13} />} onClick={() => setAddOpen(true)}>Ajouter une imprimante</Button>} /> : null}>
              {(d) => (
                <>
                  <List label="Imprimantes" items={d.printers} getKey={(p) => p.id} selectedKey={printer?.id} onSelect={(p) => setPid(p.id)}
                    render={(p) => <span className="mat-list-item"><strong>{p.name}</strong><span>{p.model_name} · {p.host}</span></span>} />
                  {printer && <PrinterCard key={printer.id} p={printer} />}
                </>
              )}
            </Async>
          </Panel>
        </div>
        <div className="prt-col prt-wide">
          <NewJob printers={plist} defaultPrinter={printer?.id} onCreated={(j) => { setJid(j.id); jobs.reload(); }} />
          <Panel title="Impressions" actions={<Button size="sm" variant="ghost" icon={<RefreshCw size={13} />} onClick={jobs.reload}>Actualiser</Button>}>
            <Async state={jobs} label="Chargement des impressions" empty={(d) => d.print_jobs.length === 0 ? <p className="hint" style={{ margin: 0 }}>Aucune impression. Découpez un modèle pour une imprimante ci-dessus.</p> : null}>
              {(d) => <List label="Impressions" items={d.print_jobs} getKey={(j) => j.id} selectedKey={jid} onSelect={(j) => setJid(j.id)}
                render={(j) => <span className="prt-job"><strong>{j.name ?? j.id}</strong><Tag tone={STATE_LABEL[j.state]?.tone}>{STATE_LABEL[j.state]?.label ?? j.state}</Tag><span className="mono dim">{fmtDate(j.updated_at)}</span></span>} />}
            </Async>
          </Panel>
          {job && <JobDetail key={job.id} job={job} printer={plist.find((p) => p.id === job.printer_id)} onChange={jobs.reload} />}
        </div>
      </div>
      <AddPrinter open={addOpen} onClose={() => setAddOpen(false)} onAdded={(p) => { setAddOpen(false); setPid(p.id); printers.reload(); }} />
    </>
  );
}

function PrinterCard({ p }: { p: PrinterView }) {
  const st = useAsync((s) => api.printerStatus(p.id, s), [p.id]);
  const reload = useRef(st.reload); reload.current = st.reload;
  useEffect(() => { const t = window.setInterval(() => reload.current(), 5000); return () => window.clearInterval(t); }, []);
  const d = st.data;
  return (
    <div className="prt-card">
      <dl className="kvl">
        <dt>Modèle</dt><dd>{p.model_name}</dd>
        <dt>Adresse</dt><dd className="mono">{p.host}</dd>
        <dt>Série</dt><dd className="mono">{p.serial}</dd>
        <dt>Buse / AMS</dt><dd className="mono">{p.nozzle_mm} mm · {p.ams_units} AMS</dd>
        <dt>Code d'accès</dt><dd>{p.access_code_set ? <Tag tone="ok">enregistré</Tag> : <Tag tone="warn">à saisir</Tag>}</dd>
      </dl>
      {!p.access_code_set && p.setup_url && <Notice title="Code d'accès non saisi" actions={<a className="btn btn-secondary btn-sm" href={assetUrl(p.setup_url)} target="_blank" rel="noreferrer"><ExternalLink size={13} /> Ouvrir la page de saisie</a>}>La page locale du serveur affiche l'empreinte du certificat présenté par l'imprimante et recueille le code d'accès ; il n'est jamais renvoyé ni affiché dans cette interface. Tant que rien n'est épinglé, aucune connexion n'est tentée (navigateur enregistré requis).</Notice>}
      {p.access_code_set && <CameraPanel printerId={p.id} />}
      <div className="prt-status" role="status">
        {st.error && !d ? <StatusDot status="offline" label="Statut indisponible" /> : !d ? <span className="hint">Lecture de l'état…</span> : (
          <>
            <StatusDot status={d.reachable ? 'online' : 'offline'} label={d.reachable ? `Joignable · ${d.state}` : 'Injoignable'} />
            {d.error && <p className="hint" style={{ margin: 0 }}>{d.error}</p>}
            {d.reachable && <p className="hint mono" style={{ margin: 0 }}>buse {Math.round(d.temps.nozzle)}/{Math.round(d.temps.nozzle_target)} °C · plateau {Math.round(d.temps.bed)}/{Math.round(d.temps.bed_target)} °C{d.total_layers > 0 && ` · couche ${d.layer}/${d.total_layers}`}{d.hms_count > 0 && ` · ${d.hms_count} alerte(s) HMS`}</p>}
            {d.updated_at && <p className="hint" style={{ margin: 0 }}>Relevé à {fmtDate(d.updated_at)} : un état ancien n'autorise jamais un lancement.</p>}
          </>
        )}
      </div>
    </div>
  );
}

function AddPrinter({ open, onClose, onAdded }: { open: boolean; onClose: () => void; onAdded: (p: PrinterView) => void }) {
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

function NewJob({ printers, defaultPrinter, onCreated }: { printers: PrinterView[]; defaultPrinter?: string; onCreated: (j: PrintJob) => void }) {
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
      <div className="prt-new">
        <Field label="Modèle (dernière version, profil appliqué le cas échéant)"><Select value={model} onChange={(e) => setModel(e.target.value)} options={[{ value: '', label: 'Choisir…' }, ...(models.data?.models ?? []).filter((m) => m.formats.some((f) => ['stl', 'obj', '3mf'].includes(f))).map((m) => ({ value: m.id, label: m.name }))]} /></Field>
        <Field label="Imprimante"><Select value={pr} onChange={(e) => setPrinter(e.target.value)} options={[{ value: '', label: 'Choisir…' }, ...printers.map((p) => ({ value: p.id, label: `${p.name} (${p.model_name})` }))]} /></Field>
        <Button variant="primary" disabled={!model || !pr || busy} onClick={go}>{busy ? 'Création…' : 'Découper pour cette imprimante'}</Button>
      </div>
    </Panel>
  );
}

function trays(ams: number): string[] { return ['ext', ...Array.from({ length: ams * 4 }, (_, i) => `ams${Math.floor(i / 4) + 1}:${(i % 4) + 1}`)]; }

function JobDetail({ job, printer, onChange }: { job: PrintJob; printer?: PrinterView; onChange: () => void }) {
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

        {job.state === 'SLICING' && <p role="status"><span className="spinner" aria-hidden /> Bambu Studio découpe le modèle…</p>}

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
        {job.state === 'UPLOADING' && <p role="status"><span className="spinner" aria-hidden /> Transfert vers l'imprimante…</p>}
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
            <div className="progress" role="progressbar" aria-valuenow={pg?.progress_percent ?? job.print?.percent ?? 0} aria-valuemin={0} aria-valuemax={100} aria-label="Avancement de l'impression"><span style={{ width: `${pg?.progress_percent ?? job.print?.percent ?? 0}%` }} /></div>
            <p className="hint mono" style={{ margin: 'var(--sp-1) 0 0' }}>{pg?.progress_percent ?? job.print?.percent ?? 0} % · couche {pg?.layer ?? job.print?.layer ?? 0}/{pg?.total_layers ?? job.print?.total_layers ?? 0} · reste {pg?.remaining_minutes ?? job.print?.remaining_minutes ?? '?'} min</p>
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
