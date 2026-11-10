import { useEffect, useMemo, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import { ArrowUpRight, Box, Camera, Plus, Printer as PrinterIcon, ShieldCheck } from 'lucide-react';
import { EmptyState, ProgressBar } from '../components';
import { api, assetUrl, BASE } from '../api/client';
import { useAsync, useConnection } from '../api/hooks';
import { insightsApi } from '../api/insights';
import type { LogEntry } from '../api/insights';
import { filamentApi } from '../api/filamentApi';
import type { QueueEntry } from '../api/filamentApi';
import type { PrintJob, PrinterStatus, PrinterView } from '../api/printTypes';
import { finishAt, formatEta, formatMinutes, printSource } from '../api/progress';
import { PRODUCT_NAME, TAGLINE } from '../brand';
import { Async, FormatBadge, fmtBytes, fmtDate, fmtNum } from './common';
import { isPrinting, usePrinterStatus } from './Printers';
import { LaunchButton, stateText } from './Print';
import { logOutcomeLabel, printerPill, queueStateLabel } from '@shared/labels';
import './screens.css';

const SHORTCUTS = [
  { to: '/bibliotheque', title: 'Retrouver ou importer', text: 'Recherche, collections, filtres de format, import non destructif.' },
  { to: '/impressions/nouvelle', title: 'Nouvelle impression', text: 'Choisir un modèle, préciser le résultat voulu, préparer l\'impression.' },
  { to: '/projets', title: 'Projets', text: 'Regrouper les pièces d\'un même objet et suivre son avancement.' },
  { to: '/matieres', title: 'Matières', text: 'Bobines, couleurs, fiches techniques ; l\'inconnu reste inconnu.' },
  { to: '/impressions', title: 'Impressions', text: 'Ce qui est à faire, en cours, terminé ; confirmation explicite avant tout lancement.' },
  { to: '/impressions?vue=bilan', title: 'Bilan et coûts', text: 'Matière, électricité, usure : estimations à partir de vos tarifs.' },
] as const;

const money = (v: number, cur = 'EUR') => `${fmtNum(v, 2)} ${cur === 'EUR' ? '€' : cur}`;
const hm = (s?: number) => formatEta(s);

const monthRange = () => {
  const to = new Date();
  const from = new Date(to.getFullYear(), to.getMonth(), 1);
  const iso = (d: Date) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
  return { from: iso(from), to: iso(to) };
};

/** Photo du plateau : instantané du serveur, rafraîchissable à la demande (le flux n'est jamais ouvert en arrière-plan). */
function Snapshot({ printer, live }: { printer: PrinterView; live: boolean }) {
  const [stamp, setStamp] = useState(() => Date.now());
  const [failed, setFailed] = useState(false);
  if (!printer.access_code_set) return <div className="live-cam live-cam-empty"><Camera aria-hidden /><span>Caméra disponible une fois le code d'accès saisi.</span></div>;
  return (
    <div className="live-cam">
      {failed
        ? <div className="live-cam-empty"><Camera aria-hidden /><span>Caméra indisponible pour cette imprimante.</span></div>
        : <img src={`${BASE}/api/printers/${encodeURIComponent(printer.id)}/camera/snapshot?t=${stamp}`} alt={`Photo du plateau de ${printer.name}`} onError={() => setFailed(true)} onLoad={() => setFailed(false)} />}
      {live && !failed && <span className="rec"><i />Dernière photo</span>}
      <button type="button" className="btn btn-sm cam-btn" onClick={() => { setFailed(false); setStamp(Date.now()); }}><Camera size={14} aria-hidden />Photo</button>
    </div>
  );
}

function LivePrinter({ printer, activeJob }: { printer: PrinterView; activeJob?: PrintJob }) {
  const st = usePrinterStatus(printer.id, 5000);
  const d: PrinterStatus | undefined = st.data;
  const pill = printerPill(d, !!st.error);
  const printing = isPrinting(d);
  const eta = d && d.remaining_minutes > 0 ? d.remaining_minutes * 60 : undefined;
  const title = activeJob?.name ?? d?.current_file;
  return (
    <section className="panel" aria-label={`Imprimante ${printer.name} en direct`}>
      <div className="panel-h">
        <div className="prn-id"><div className="ico"><PrinterIcon aria-hidden /></div><div><h3>{printer.name}</h3><div className="sub mono">{printer.host}</div></div></div>
        <span className={`pill ${pill.cls}`} role="status"><i />{pill.label}</span>
      </div>
      <div className="live">
        <div>
          {printing && d ? (
            <>
              {title && <div className="live-title">{title}</div>}
              <ProgressBar large label={`Impression en cours sur ${printer.name}`} source={{ state: 'running', percent: d.progress_percent, remaining_minutes: d.remaining_minutes, phase_label: d.total_layers > 0 ? `couche ${d.layer} sur ${d.total_layers}` : undefined }} hideEta />
              <dl className="kv2">
                <div><dt>Temps restant</dt><dd>{formatMinutes(d.remaining_minutes) ?? 'inconnu'}</dd></div>
                <div><dt>Fin estimée</dt><dd>{finishAt(eta) ?? 'inconnue'}</dd></div>
                <div><dt>Buse</dt><dd className="mono">{Math.round(d.temps.nozzle)} / {Math.round(d.temps.nozzle_target)} °C</dd></div>
                <div><dt>Plateau</dt><dd className="mono">{Math.round(d.temps.bed)} / {Math.round(d.temps.bed_target)} °C</dd></div>
              </dl>
            </>
          ) : (
            <>
              <div className="live-title">{!d ? 'Lecture de l\'état…' : !d.reachable ? 'Imprimante injoignable' : 'Aucune impression en cours'}</div>
              {d?.display && <p className="sub">{d.display}</p>}
              {d?.explanation && <p className="sub">{d.explanation}</p>}
              {d?.error && <p className="sub">{d.error}</p>}
              {d?.reachable && <dl className="kv2">
                <div><dt>Buse</dt><dd className="mono">{Math.round(d.temps.nozzle)} / {Math.round(d.temps.nozzle_target)} °C</dd></div>
                <div><dt>Plateau</dt><dd className="mono">{Math.round(d.temps.bed)} / {Math.round(d.temps.bed_target)} °C</dd></div>
                {d.last_job && <div><dt>Dernière impression</dt><dd>{d.last_job.name}</dd></div>}
                {d.hms_count > 0 && <div><dt>Alertes de l'imprimante</dt><dd>{d.hms_count}</dd></div>}
              </dl>}
              {d?.reachable && <p className="sub">Prête : choisissez un modèle, préparez-le, puis lancez après confirmation.</p>}
            </>
          )}
          {d?.updated_at && <p className="sub live-stamp">Relevé à {fmtDate(d.updated_at)}</p>}
        </div>
        <Snapshot printer={printer} live={printing} />
      </div>
    </section>
  );
}

function QueuePanel() {
  const q = useAsync((s) => filamentApi.queue(false, s), []);
  const reload = useRef(q.reload); reload.current = q.reload;
  useEffect(() => { const t = window.setInterval(() => reload.current(), 8000); return () => window.clearInterval(t); }, []);
  const entries: QueueEntry[] = (q.data?.queue ?? []).filter((e) => !['done', 'failed', 'removed'].includes(e.state));
  return (
    <section className="panel" aria-label="File d'attente">
      <div className="panel-h"><h3>File d'attente</h3>{entries.length > 0 ? <span className="pill wait"><i />{entries.length} en attente</span> : <Link className="btn btn-ghost btn-sm" to="/impressions?vue=en-cours">Gérer</Link>}</div>
      <Async state={q} label="Chargement de la file" empty={() => entries.length === 0 ? <p className="sub">Aucune impression en file. Ajoutez-en depuis <Link to="/impressions?vue=en-cours">la file d'attente</Link>.</p> : null}>
        {() => (
          <ul className="rowlist">
            {entries.map((e) => (
              <li key={e.id}>
                <span className="th" aria-hidden><Box size={20} /></span>
                <div className="grow"><b>{e.file_name ?? e.print_job_id}</b><small>{[e.estimated_minutes ? formatMinutes(e.estimated_minutes) : undefined, e.estimated_grams ? `${fmtNum(e.estimated_grams, 0)} g` : undefined, queueStateLabel(e.state).label].filter(Boolean).join(' · ')}</small></div>
                {e.state === 'ready' && e.confirm_url && <a className="btn btn-primary btn-sm" href={assetUrl(e.confirm_url)} title="Ouvre la page de confirmation : vous y approuvez le lancement"><ShieldCheck size={13} aria-hidden />Vérifier et lancer</a>}
              </li>
            ))}
          </ul>
        )}
      </Async>
      <p className="sub panel-foot">Rien ne démarre seul : chaque lancement passe par votre confirmation.</p>
    </section>
  );
}

function LongJobs({ jobs, prog, printers }: { jobs: PrintJob[]; prog: Record<string, import('../api/printTypes').JobProgress>; printers: PrinterView[] }) {
  const longs = jobs.filter((j) => ['PREPARED', 'SLICING', 'UPLOADING'].includes(j.state));
  if (longs.length === 0) return null;
  return (
    <section className="panel" aria-label="Préparations en cours" style={{ marginTop: 'var(--sp-6)' }}>
      <div className="panel-h"><h3>Préparations en cours</h3><span className="pill run"><i />{longs.length}</span></div>
      <ul className="rowlist">
        {longs.map((j) => (
          <li key={j.id} className="long-job">
            <span className="th" aria-hidden><Box size={20} /></span>
            <div className="grow"><b>{j.name ?? j.id}</b><small>{printers.find((p) => p.id === j.printer_id)?.name ?? j.printer_id} · {stateText(j, prog[j.id])}</small></div>
            <div className="long-bar"><ProgressBar label={`Avancement : ${j.name ?? j.id}`} source={printSource(j, prog[j.id])} /></div>
          </li>
        ))}
      </ul>
    </section>
  );
}

function Recent() {
  const log = useAsync((s) => insightsApi.log({ limit: 4 }, s), []);
  return (
    <section className="panel flush" aria-label="Dernières impressions" style={{ marginTop: 'var(--sp-6)' }}>
      <div className="panel-h panel-h-pad"><h3>Dernières impressions</h3><Link className="btn btn-ghost btn-sm" to="/impressions?vue=terminees">Tout voir</Link></div>
      <Async state={log} label="Chargement du journal" empty={(d) => d.entries.length === 0 ? <p className="sub panel-foot">Le journal se remplit à la fin de chaque impression.</p> : null}>
        {(d) => (
          <ul className="jobs">
            {d.entries.map((e: LogEntry) => {
              const o = logOutcomeLabel(e.outcome);
              const secs = e.actual.seconds ?? e.estimated.seconds;
              const grams = e.actual.grams ?? e.estimated.grams;
              const cost = (e.actual_cost ?? e.planned_cost)?.total;
              return (
                <li key={e.id} className="job job-log">
                  <span className="job-th" aria-hidden><Box size={22} /></span>
                  <div className="job-main"><b>{e.name ?? e.model_name ?? e.print_job_id}</b><span className="job-note">{e.comment ?? `${e.printer_model ?? e.printer_id ?? ''} · ${fmtDate(e.finished_at)}`}</span></div>
                  <span className="job-meta mono">{secs ? <span>{hm(secs)}</span> : null}{e.material_family && <span>{e.material_family}</span>}{grams ? <span>{fmtNum(grams, 0)} g</span> : null}{cost !== undefined && <span>{money(cost, (e.actual_cost ?? e.planned_cost)?.currency)}</span>}</span>
                  <span className={`pill ${o.pill}`}><i />{o.label}</span>
                </li>
              );
            })}
          </ul>
        )}
      </Async>
    </section>
  );
}

function RecentModels() {
  const recent = useAsync((s) => api.models({ sort: 'updated', desc: true, limit: 5 }, s), []);
  return (
    <section className="panel" aria-label="Récemment modifiés" style={{ marginTop: 'var(--sp-6)' }}>
      <div className="panel-h"><h3>Récemment modifiés</h3><Link className="btn btn-ghost btn-sm" to="/bibliotheque">Bibliothèque</Link></div>
      <Async state={recent} label="Chargement" empty={(d) => d.models.length === 0 ? <p className="sub">Le catalogue est vide. <Link to="/bibliotheque">Importer des modèles</Link>.</p> : null}>
        {(d) => (
          <ul className="rowlist">
            {d.models.map((m) => (
              <li key={m.id}><Link to={`/modele/${m.id}`} className="row-a"><FormatBadge formats={m.formats} size={40} /><span className="grow"><b>{m.name}</b><small>{m.collection_path ?? '—'} · {fmtBytes(m.size_bytes)} · {fmtDate(m.updated_at)}</small></span></Link></li>
            ))}
          </ul>
        )}
      </Async>
    </section>
  );
}

export function Home() {
  const conn = useConnection();
  const printers = useAsync((s) => api.printers(s), []);
  const jobs = useAsync((s) => api.printJobs(s, 'all'), []);
  const jobsReload = useRef(jobs.reload); jobsReload.current = jobs.reload;
  const range = useMemo(monthRange, []);
  const month = useAsync((s) => insightsApi.dashboard(range, s), [range.from, range.to]);
  const plist = printers.data?.printers ?? [];
  const all = jobs.data?.print_jobs ?? [];
  const activeJobs = all.filter((j) => ['PREPARED', 'SLICING', 'UPLOADING', 'START_REQUESTED', 'PRINTING'].includes(j.state));
  const busy = activeJobs.length > 0;
  useEffect(() => { if (!busy) return; const t = window.setInterval(() => jobsReload.current(), 4000); return () => window.clearInterval(t); }, [busy]);
  const printing = all.find((j) => j.state === 'PRINTING');
  const printer = plist.find((p) => p.id === printing?.printer_id) ?? plist[0];
  const today = new Date().toLocaleDateString('fr-FR', { weekday: 'long', day: 'numeric', month: 'long' });
  const prog = jobs.data?.progress ?? {};
  const t = month.data?.totals;
  const mats = (month.data?.by_material ?? []).slice(0, 2).map((m) => m.name).join(' et ');
  const finished = t ? t.succeeded + t.partial + t.failed : 0;
  const nPrinting = all.filter((j) => j.state === 'PRINTING').length;

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Bonjour</h1>
          <div className="sub">{today.charAt(0).toUpperCase() + today.slice(1)}{nPrinting > 0 ? ` · ${nPrinting} impression${nPrinting > 1 ? 's' : ''} en cours` : ` · ${PRODUCT_NAME}, ${TAGLINE.toLowerCase()}`}</div>
        </div>
        <div className="head-acts"><Link className="btn btn-secondary" to="/bibliotheque"><Plus size={16} aria-hidden />Importer un modèle</Link></div>
      </div>

      <div className="stats">
        <div className="panel stat"><div className="overline">Impressions du mois</div><div className="v">{t ? t.prints : '—'}</div><div className="d">{t ? `dont ${nPrinting} en cours` : 'journal indisponible'}</div></div>
        <div className="panel stat"><div className="overline">Filament consommé</div><div className="v">{t ? fmtNum(t.filament_grams / 1000, 2) : '—'}<small>kg</small></div><div className="d">{mats || 'matière'}</div></div>
        <div className="panel stat"><div className="overline">Coût du mois</div><div className="v">{t ? fmtNum(t.cost, 2) : '—'}<small>{month.data?.currency === 'EUR' || !month.data ? '€' : month.data.currency}</small></div><div className="d">estimation matière + électricité</div></div>
        <div className="panel stat"><div className="overline">Taux de réussite</div><div className="v">{t?.success_rate !== undefined ? fmtNum(t.success_rate * 100, 0) : '—'}<small>%</small></div><div className="d">{t ? `${t.succeeded} réussie${t.succeeded > 1 ? 's' : ''} sur ${finished} terminée${finished > 1 ? 's' : ''}` : ''}</div></div>
      </div>

      <div className="grid g-home">
        {printers.data && plist.length === 0
          ? <section className="panel"><EmptyState title="Aucune imprimante enregistrée" text="Ajoutez votre imprimante pour suivre l'impression en direct." action={<Link className="btn btn-primary btn-sm" to="/imprimantes"><Plus size={13} aria-hidden />Ajouter une imprimante</Link>} /></section>
          : printer ? <LivePrinter key={printer.id} printer={printer} activeJob={printing} /> : <section className="panel"><p className="sub">Chargement de l'imprimante…</p></section>}
        <QueuePanel />
      </div>

      <LongJobs jobs={all} prog={prog} printers={plist} />

      {all.some((j) => ['UPLOADED', 'AWAITING_PRINT_CONFIRMATION'].includes(j.state)) && (
        <section className="panel" aria-label="Prêtes à lancer" style={{ marginTop: 'var(--sp-6)' }}>
          <div className="panel-h"><h3>Prêtes à lancer</h3><span className="pill wait"><i />En attente de vous</span></div>
          <ul className="rowlist">
            {all.filter((j) => ['UPLOADED', 'AWAITING_PRINT_CONFIRMATION'].includes(j.state)).map((j) => (
              <li key={j.id}><span className="th" aria-hidden><Box size={20} /></span><div className="grow"><b>{j.name ?? j.id}</b><small>{plist.find((p) => p.id === j.printer_id)?.name ?? j.printer_id} · {stateText(j)}</small></div><LaunchButton job={j} /></li>
            ))}
          </ul>
          <p className="sub panel-foot">« Vérifier et lancer » ouvre la page de confirmation du serveur : vous y vérifiez la photo du plateau et approuvez.</p>
        </section>
      )}

      <Recent />
      <RecentModels />

      <h2 className="shortcuts-h">Raccourcis</h2>
      <ol className="shortcuts" aria-label="Raccourcis">
        {SHORTCUTS.map((s, i) => (
          <li key={s.title}><Link to={s.to} className="panel shortcut"><span className="n mono" aria-hidden>{i + 1}</span><span className="t"><b>{s.title}</b><small>{s.text}</small></span><ArrowUpRight size={15} aria-hidden /></Link></li>
        ))}
      </ol>
      {conn.server === 'online' && !conn.importEnabled && conn.importNote && <p className="sub" style={{ marginTop: 'var(--sp-4)' }}>{conn.importNote}</p>}
    </>
  );
}
