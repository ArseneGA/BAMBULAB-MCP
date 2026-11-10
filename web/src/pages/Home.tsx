import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { AlertTriangle, Bell, Box, CheckCircle2, FolderKanban, Plus, ShieldCheck, TriangleAlert, Upload } from 'lucide-react';
import { Button, EmptyState, ErrorState, Skeleton } from '../components';
import { VerdictBar, useVerdictFlow } from '../components/VerdictBar';
import { api, ApiError } from '../api/client';
import { useAsync, useConnection, isOffline } from '../api/hooks';
import type { AsyncState } from '../api/hooks';
import { insightsApi } from '../api/insights';
import type { LogEntry } from '../api/insights';
import { filamentApi } from '../api/filamentApi';
import { dashboardApi } from '../api/dashboardApi';
import type { CameraAlert } from '../api/dashboardApi';
import { projectsApi, STATUS_LABEL, VERDICT_LABEL } from '../api/projectsTypes';
import type { ListItem, PendingList, ProjectDetail } from '../api/projectsTypes';
import type { PrintJob, PrinterView } from '../api/printTypes';
import { formatEta } from '../api/progress';
import { PRODUCT_NAME } from '../brand';
import { fmtNum } from './common';
import { LiveCard } from './dashboard/LiveCard';
import type { LiveReport } from './dashboard/LiveCard';
import { TODO_MAX, ageText, buildTodo, dormantProjects, fmtTime, isPrinted, judgedText, lowSpools, monthStats, printingTitle, readDormantDays, useVisiblePolling } from './dashboard/logic';
import type { TodoItem } from './dashboard/logic';
import { logOutcomeLabel, printStateLabel } from '@shared/labels';
import './dashboard/dashboard.css';

/* ---------- briques communes ---------- */

/** Lecture tolérante : une route absente (404, G9 pas encore là) ou en erreur donne `undefined`, jamais une erreur d'écran. */
const soft = <T,>(p: Promise<T>): Promise<T | undefined> => p.catch((e) => { if (e instanceof ApiError || (e as Error).name !== 'AbortError') return undefined; throw e; });

const ACTIVE = ['PREPARED', 'SLICING', 'UPLOADING', 'START_REQUESTED', 'PRINTING'];
const money = (v: number, cur = 'EUR') => `${fmtNum(v, 2)} ${cur === 'EUR' ? '€' : cur}`;

/** Heure de la dernière donnée reçue pour un bloc (« Mis à jour à 14:32 »). */
function useStamp(data: unknown): Date | undefined {
  const seen = useRef<unknown>(undefined);
  const at = useRef<Date | undefined>(undefined);
  if (data !== undefined && data !== seen.current) { seen.current = data; at.current = new Date(); }
  return at.current;
}

/**
 * Un bloc de l'Accueil, indépendant des autres : squelette au chargement, erreur lisible en trois parties, ou — si le service ne répond plus —
 * la dernière valeur connue avec « Dernière mise à jour à… » (le bandeau d'erreur est unique, celui de la coque).
 */
function Block<T>({ id, title, state, actions, children, empty, foot }: { id: string; title: string; state: AsyncState<T>; actions?: ReactNode; children: (d: T) => ReactNode; empty?: (d: T) => ReactNode | null; foot?: ReactNode }) {
  const at = useStamp(state.data);
  let body: ReactNode;
  if (state.data === undefined) {
    if (state.error) body = isOffline(state.error)
      ? <p className="sub" role="status">Indisponible pour l'instant : le service ne répond pas.</p>
      : <ErrorState title="Cette partie n'a pas pu se charger" text="La lecture a échoué." impact="Le reste de l'Accueil reste utilisable ; rien n'a été modifié." action={<Button size="sm" onClick={state.reload}>Réessayer</Button>} details={state.error.message} />;
    else body = <Skeleton lines={3} label={`Chargement : ${title}`} />;
  } else body = empty?.(state.data) ?? children(state.data);
  return (
    <section className="panel dash-block" aria-label={title} id={id} aria-busy={state.data === undefined && !state.error}>
      <div className="panel-h"><h2 className="dash-h">{title}</h2>{actions}</div>
      {body}
      {state.error && state.data !== undefined && <p className="sub dash-stale" role="status">Service injoignable : dernière mise à jour à {fmtTime(at)}.</p>}
      {foot}
    </section>
  );
}

/* ---------- 0. Salutation ---------- */
function Greeting({ printing, eta }: { printing: number; eta?: string }) {
  const today = new Date().toLocaleDateString('fr-FR', { weekday: 'long', day: 'numeric', month: 'long' });
  return (
    <div>
      <h1>Bonjour</h1>
      <p className="sub">{today.charAt(0).toUpperCase() + today.slice(1)} · {printing > 0 ? `${printing} impression${printing > 1 ? 's' : ''} en cours${eta ? `, fin vers ${eta}` : ''}` : 'Tout est calme.'}</p>
    </div>
  );
}

/* ---------- 1. À faire ---------- */
const TODO_ICON = { confirm: ShieldCheck, alert: TriangleAlert, verdict: CheckCircle2, spool: Bell, dormant: FolderKanban } as const;
const TODO_KIND = { confirm: 'À confirmer', alert: 'Alerte', verdict: 'Verdict', spool: 'Bobine basse', dormant: 'Projet dormant' } as const;

function TodoBlock({ items, reasons, onChange }: { items: TodoItem[]; reasons: string[]; onChange: () => void }) {
  const [all, setAll] = useState(false);
  const flow = useVerdictFlow(onChange);
  const shown = all ? items : items.slice(0, TODO_MAX);
  return (
    <>
      <section className="panel dash-block dash-todo" aria-label="À faire" id="a-faire">
        <div className="panel-h"><h2 className="dash-h">À faire</h2><span className="pill wait"><i />{items.length}</span></div>
        <ul className="dash-todo-list">
          {shown.map((t) => {
            const I = TODO_ICON[t.kind];
            return (
              <li key={t.key} className={`dash-todo-row dash-todo-${t.kind}`}>
                <span className="dash-todo-ico"><I size={18} aria-hidden /></span>
                <div className="grow"><b>{t.title}</b><small><span className="dash-kind">{TODO_KIND[t.kind]}</span> · {t.detail}</small></div>
                {t.kind === 'confirm' && t.href && (t.server
                  ? <a className="btn btn-primary btn-sm" href={t.href} title="Ouvre la page de confirmation du serveur : c'est là que vous approuvez le lancement. Ce lien ne lance rien."><ShieldCheck size={13} aria-hidden />Vérifier et lancer</a>
                  : <Link className="btn btn-secondary btn-sm" to={t.href}>Ouvrir la fiche</Link>)}
                {t.kind === 'verdict' && t.verdict && <VerdictBar label={t.title} reasons={reasons} busy={flow.busy} onGive={(v, r) => flow.give(t.verdict!.print_job_id, t.title, v, r)} />}
                {(t.kind === 'alert' || t.kind === 'spool' || t.kind === 'dormant') && t.href && <Link className="btn btn-secondary btn-sm" to={t.href}>{t.kind === 'alert' ? 'Examiner' : 'Ouvrir'}</Link>}
              </li>
            );
          })}
        </ul>
        {items.length > TODO_MAX && <Button size="sm" variant="ghost" onClick={() => setAll((a) => !a)}>{all ? 'Réduire' : `Voir tout (${items.length})`}</Button>}
      </section>
      {flow.bar}
    </>
  );
}

/* ---------- 3. Projets en cours ---------- */
function ProjectCard({ item }: { item: ListItem }) {
  const detail = useAsync((s) => soft<ProjectDetail>(projectsApi.get(item.project.id, s)), [item.project.id]);
  const p = detail.data?.progress ?? item.progress;
  const pct = p.units_total > 0 ? Math.round((p.units_printed / p.units_total) * 100) : 0;
  const extra = p.text.includes('dont') || p.units_printed === 0 ? '' : ` · dont ${p.units_judged_success} jugée${p.units_judged_success > 1 ? 's' : ''} réussie${p.units_judged_success > 1 ? 's' : ''}${p.units_unjudged > 0 ? `, ${p.units_unjudged} non jugée${p.units_unjudged > 1 ? 's' : ''}` : ''}`;
  const cost = detail.data?.totals;
  return (
    <li className="dash-proj">
      <div className="dash-proj-h"><b>{item.project.name}</b><span className={`pill ${item.project.status === 'printing' ? 'run' : ''}`}><i />{STATUS_LABEL[item.project.status]}</span></div>
      <div className="dash-bar"><div className="dash-track" role="meter" aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct} aria-label={`Avancement de ${item.project.name} : ${p.text}`}><span style={{ width: `${pct}%` }} /></div></div>
      <p className="sub">{p.text}{extra}</p>
      {detail.data?.next_action && <p className="sub">Prochaine action : {detail.data.next_action}</p>}
      {cost?.available && cost.actual_cost > 0 && <p className="sub">Coût à date : {money(cost.actual_cost, cost.currency)}</p>}
    </li>
  );
}

/* ---------- 4. Dernières impressions ---------- */
function RecentRow({ e, reasons, flow }: { e: LogEntry; reasons: string[]; flow: ReturnType<typeof useVerdictFlow> }) {
  const [edit, setEdit] = useState(false);
  const o = logOutcomeLabel(e.outcome);
  const secs = e.actual.seconds ?? e.estimated.seconds;
  const grams = e.actual.grams ?? e.estimated.grams;
  const name = e.name ?? e.model_name ?? e.print_job_id;
  const verdict = e.verdict || undefined;
  return (
    <li className="job job-log">
      <span className="job-th" aria-hidden><Box size={22} /></span>
      <div className="job-main"><Link to={`/impressions/${encodeURIComponent(e.print_job_id)}`}><b>{name}</b></Link><span className="job-note">{e.printer_model ?? e.printer_id ?? ''}</span></div>
      <span className="job-meta mono">{secs ? <span>{formatEta(secs)}</span> : null}{e.material_family && <span>{e.material_family}</span>}{grams ? <span>{fmtNum(grams, 0)} g</span> : null}</span>
      <span className={`pill ${o.pill}`}><i />{o.label}</span>
      {isPrinted(e) && (verdict && !edit
        ? <span className="verdict-pill"><span className={`pill ${verdict === 'success' ? 'ok' : verdict === 'failed' ? 'fail' : 'wait'}`}><i />{VERDICT_LABEL[verdict]}</span><Button size="sm" variant="ghost" onClick={() => setEdit(true)}>Modifier</Button></span>
        : <VerdictBar label={name} reasons={reasons} busy={flow.busy} onGive={(v, r) => { setEdit(false); flow.give(e.print_job_id, name, v, r); }} />)}
    </li>
  );
}

/* ---------- l'Accueil ---------- */
export function Home() {
  const conn = useConnection();
  const printers = useAsync((s) => api.printers(s), []);
  const jobs = useAsync((s) => api.printJobs(s, 'all'), []);
  const log = useAsync((s) => insightsApi.log({ limit: 200 }, s), []);
  const range = useMemo(() => { const to = new Date(); const from = new Date(to.getFullYear(), to.getMonth(), 1); const iso = (d: Date) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`; return { from: iso(from), to: iso(to) }; }, []);
  const month = useAsync((s) => insightsApi.dashboard(range, s), [range.from, range.to]);
  const pending = useAsync((s) => soft<PendingList>(projectsApi.pending(s)), []);
  const projects = useAsync((s) => soft(projectsApi.list({}, s)), []);
  const spools = useAsync((s) => filamentApi.spools({}, s), []);
  const queue = useAsync((s) => filamentApi.queue(false, s), []);
  const models = useAsync((s) => api.models({ limit: 1 }, s), []);

  // Sondages : seulement onglet visible (Page Visibility), reprise immédiate au retour.
  const refresh = useRef<() => void>(() => undefined);
  refresh.current = () => { jobs.reload(); pending.reload(); log.reload(); };
  useVisiblePolling(() => refresh.current(), 4000);
  const reloadAll = useCallback(() => { refresh.current(); month.reload(); projects.reload(); }, []); // eslint-disable-line react-hooks/exhaustive-deps

  const plist = printers.data?.printers ?? [];
  const all = jobs.data?.print_jobs ?? [];
  const printingJobs = all.filter((j) => j.state === 'PRINTING');
  const busy = all.filter((j) => ACTIVE.includes(j.state));

  // État des imprimantes remonté par les cartes : titre d'onglet, fin estimée, alertes.
  const [reports, setReports] = useState<Record<string, LiveReport>>({});
  const onReport = useCallback((r: LiveReport) => setReports((m) => (m[r.printerId]?.status === r.status && m[r.printerId]?.failed === r.failed ? m : { ...m, [r.printerId]: r })), []);
  const live = Object.values(reports).map((r) => r.status).filter((s) => s && s.reachable && ['printing', 'paused'].includes(s.state)) as NonNullable<LiveReport['status']>[];
  const first = live[0];
  const firstJob = printingJobs.find((j) => j.printer_id === first?.printer_id);
  useEffect(() => {
    if (!first) return;
    document.title = printingTitle(first.progress_percent, firstJob?.name ?? first.current_file, PRODUCT_NAME);
    return () => { document.title = PRODUCT_NAME; };
  }, [first?.progress_percent, firstJob?.name, first?.current_file]); // eslint-disable-line react-hooks/exhaustive-deps
  const etaText = first && first.remaining_minutes > 0 ? new Date(Date.now() + first.remaining_minutes * 60_000).toLocaleTimeString('fr-FR', { hour: '2-digit', minute: '2-digit' }) : undefined;

  // Alertes de la caméra pour les impressions en cours.
  const [camAlerts, setCamAlerts] = useState<Array<{ job: PrintJob; alert: CameraAlert }>>([]);
  const pjKey = printingJobs.map((j) => j.id).join(',');
  const loadAlerts = useCallback(() => {
    Promise.all(printingJobs.map((job) => soft(dashboardApi.jobAlerts(job.id)).then((r) => ({ job, alert: r?.alerts?.[0] })))).then((rs) => setCamAlerts(rs.filter((x): x is { job: PrintJob; alert: CameraAlert } => !!x.alert)));
  }, [pjKey]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { loadAlerts(); }, [loadAlerts]);
  useVisiblePolling(loadAlerts, 30_000, printingJobs.length > 0);

  const dormantDays = readDormantDays();
  const items = projects.data?.projects ?? [];
  const roots = items.filter((i) => !i.project.parent_id && !i.project.archived && !['done', 'in_use', 'abandoned'].includes(i.project.status));
  const shownProjects = [...roots].sort((a, b) => Number(b.project.status === 'printing') - Number(a.project.status === 'printing') || (b.project.updated_at ?? '').localeCompare(a.project.updated_at ?? '')).slice(0, 3);
  const queueEntries = (queue.data?.queue ?? []).filter((e) => !['done', 'failed', 'removed'].includes(e.state));
  const low = lowSpools(spools.data?.spools ?? []);
  const spoolsNeeded = low.flatMap((s) => {
    const q = queueEntries.find((e) => (e.assignments ?? []).some((a) => a.tray === s.slot) && e.printer_id === s.printer_id);
    return q ? [{ name: q.file_name ?? q.print_job_id, spool: s }] : [];
  });
  const alertItems = [
    ...camAlerts.map((c) => ({ job: c.job, summary: `Alerte de la caméra · ${ageText(c.alert.at)}`.replace(/ · $/, ''), at: c.alert.at })),
    ...all.filter((j) => j.state === 'FAILED' && !j.archived && !j.trashed && Date.now() - new Date(j.updated_at).getTime() < 3 * 86_400_000).map((j) => ({ job: j, summary: `${printStateLabel(j.state).label} · ${ageText(j.updated_at)}`, at: j.updated_at })),
  ];
  const todo = buildTodo({ jobs: all, pending: pending.data?.pending ?? [], alerts: alertItems, spoolsNeeded, dormant: dormantProjects(items, dormantDays) });
  const reasons = (pending.data?.fail_reasons ?? []).map((r) => r.id);
  /** Action principale de l'Accueil : un lien vers la page de confirmation du serveur s'il y en a une en attente. */
  const mainConfirm = todo.find((t) => t.kind === 'confirm' && t.server && t.href);

  const stats = monthStats(log.data?.entries ?? []);
  const t = month.data?.totals;
  const pct = stats.judged > 0 ? Math.round((stats.success / stats.judged) * 100) : undefined;
  const recentFlow = useVerdictFlow(reloadAll);
  const firstRun = printers.data && models.data && plist.length === 0 && models.data.total === 0;

  // Imprimante qui imprime d'abord, puis par nom (ordre stable).
  const sorted: PrinterView[] = [...plist].sort((a, b) => Number(!!reports[b.id]?.status && ['printing', 'paused'].includes(reports[b.id].status!.state)) - Number(!!reports[a.id]?.status && ['printing', 'paused'].includes(reports[a.id].status!.state)) || a.name.localeCompare(b.name));
  const printerAlert = live.find((s) => s.hms_count > 0 || s.print_error > 0 || s.state === 'failed');
  const alertJob = camAlerts[0]?.job ?? firstJob;
  const showAlert = camAlerts.length > 0 || !!printerAlert || Object.values(reports).some((r) => r.failed && printingJobs.some((j) => j.printer_id === r.printerId));

  return (
    <>
      <div className="page-head">
        <Greeting printing={busy.filter((j) => j.state === 'PRINTING').length} eta={etaText} />
        <div className="head-acts">
          {mainConfirm
            ? <a className="btn btn-primary" href={mainConfirm.href} title="Ouvre la page de confirmation du serveur : c'est là que vous approuvez le lancement. Ce lien ne lance rien."><ShieldCheck size={16} aria-hidden />Vérifier et lancer</a>
            : <Link className="btn btn-primary" to="/impressions/nouvelle"><Plus size={16} aria-hidden />Nouvelle impression</Link>}
        </div>
      </div>

      {showAlert && (
        <div className="dash-alert" role="alert">
          <AlertTriangle size={20} aria-hidden />
          <div className="grow"><b>{camAlerts.length > 0 ? 'La caméra signale un problème sur l\'impression en cours' : printerAlert ? 'L\'imprimante signale une alerte' : 'L\'imprimante ne répond plus pendant une impression'}</b>
            <p>Examinez le plateau. Rien ne s'arrête tout seul : l'arrêt se valide sur une page du serveur, depuis la fiche de l'impression.</p></div>
          {alertJob && <Link className="btn btn-danger btn-sm" to={`/impressions/${encodeURIComponent(alertJob.id)}`}>Examiner et arrêter…</Link>}
        </div>
      )}

      {todo.length > 0 && <TodoBlock items={todo} reasons={reasons} onChange={reloadAll} />}

      <div className="dash-live-zone" aria-label="En direct" role="region">
        <div className="panel-h dash-live-h"><h2 className="dash-h">En direct</h2></div>
        {printers.data === undefined
          ? (printers.error ? (isOffline(printers.error) ? <p className="sub" role="status">Indisponible pour l'instant : le service ne répond pas.</p> : <ErrorState title="Les imprimantes n'ont pas pu être lues" text="La lecture a échoué." impact="Le reste de l'Accueil reste utilisable." action={<Button size="sm" onClick={printers.reload}>Réessayer</Button>} />) : <Skeleton lines={3} label="Chargement des imprimantes" />)
          : plist.length === 0
            ? <section className="panel"><EmptyState title="Aucune imprimante enregistrée" text="Ajoutez votre imprimante pour la suivre en direct : état, photo du plateau, progression." action={<Link className="btn btn-primary btn-sm" to="/imprimantes"><Plus size={13} aria-hidden />Ajouter une imprimante</Link>} /></section>
            : <div className="dash-live-grid">{sorted.map((p, i) => <LiveCard key={p.id} printer={p} job={printingJobs.find((j) => j.printer_id === p.id)} big={i === 0 || !!reports[p.id]?.status && ['printing', 'paused'].includes(reports[p.id].status!.state)} onReport={onReport} />)}</div>}
      </div>

      {!firstRun && !projects.error && projects.data !== undefined && (
        <Block id="projets" title="Projets en cours" state={projects} actions={<Link className="btn btn-ghost btn-sm" to="/projets">Tous les projets</Link>}
          empty={() => shownProjects.length === 0 ? <EmptyState title="Aucun projet en cours" text="Un projet regroupe les pièces d'un même objet et suit son avancement." action={<Link className="btn btn-secondary btn-sm" to="/projets">Créer un projet</Link>} /> : null}>
          {() => <ul className="dash-projs">{shownProjects.map((i) => <ProjectCard key={i.project.id} item={i} />)}</ul>}
        </Block>
      )}

      <Block id="dernieres" title="Dernières impressions" state={log} actions={<Link className="btn btn-ghost btn-sm" to="/impressions?vue=terminees">Tout voir</Link>}
        empty={(d) => d.entries.length === 0 ? <p className="sub">Le journal se remplit à la fin de chaque impression.</p> : null}>
        {(d) => <ul className="jobs">{d.entries.slice(0, 5).map((e) => <RecentRow key={e.id} e={e} reasons={reasons} flow={recentFlow} />)}</ul>}
      </Block>
      {recentFlow.bar}

      <Block id="chiffres" title="Chiffres du mois" state={month} actions={<Link className="btn btn-ghost btn-sm" to="/impressions?vue=bilan">Bilan</Link>}>
        {() => (
          <div className="stats dash-stats">
            <div className="stat" title="Mesuré : impressions du journal arrivées à 100 % ce mois-ci"><div className="overline">Impressions</div><div className="v">{stats.printed}</div><div className="d">terminées ce mois-ci · <span className="lvl">mesuré</span></div></div>
            <div className="stat" title="Calculé : réussies jugées / jugées. Les impressions non jugées sont dites, jamais comptées dans le taux."><div className="overline">Taux de réussite</div><div className="v">{pct !== undefined ? pct : 0}<small>%</small></div><div className="d">{judgedText(stats)} · <span className="lvl">calculé</span></div></div>
            <div className="stat" title="Mesuré : durées réelles du journal"><div className="overline">Heures machine</div><div className="v">{fmtNum(t?.machine_hours ?? 0, 1)}<small>h</small></div><div className="d">{(t?.prints ?? 0) === 0 ? 'aucune impression ce mois-ci' : 'durées réelles'} · <span className="lvl">mesuré</span></div></div>
            <div className="stat" title="Estimé : matière et électricité d'après vos tarifs"><div className="overline">Matière et coût</div><div className="v">{fmtNum((t?.filament_grams ?? 0) / 1000, 2)}<small>kg · {money(t?.cost ?? 0, month.data?.currency)}</small></div><div className="d">d'après vos tarifs · <span className="lvl">estimé</span></div></div>
          </div>
        )}
      </Block>

      {low.length > 0 && (
        <section className="panel dash-block" aria-label="Bobines basses" id="bobines-basses">
          <div className="panel-h"><h2 className="dash-h">Bobines basses</h2><Link className="btn btn-ghost btn-sm" to="/matieres">Ouvrir Matières</Link></div>
          <ul className="dash-spools">{low.map((s) => <li key={s.id}><span className="swatch" style={{ background: s.color_hex ? `#${s.color_hex.replace('#', '')}` : 'var(--well)' }} aria-hidden /><b>{s.name || s.material}</b><span className="sub">il reste ~{Math.round(s.remaining_g)} g · {s.status === 'empty' ? 'vide' : 'presque vide'}</span></li>)}</ul>
        </section>
      )}

      {firstRun ? (
        <section className="panel dash-block" aria-label="Premiers pas">
          <div className="panel-h"><h2 className="dash-h">Pour commencer</h2></div>
          <ol className="dash-steps">
            <li><span className="n" aria-hidden>1</span><div className="grow"><b>Ajouter une imprimante</b><small>Son modèle, son adresse sur le réseau local.</small></div><Link className="btn btn-primary btn-sm" to="/imprimantes">Ajouter</Link></li>
            <li><span className="n" aria-hidden>2</span><div className="grow"><b>Importer un modèle</b><small>Un fichier 3D de votre choix ; l'original n'est jamais modifié.</small></div><Link className="btn btn-secondary btn-sm" to="/bibliotheque">Importer</Link></li>
            <li><span className="n" aria-hidden>3</span><div className="grow"><b>Préparer une impression</b><small>Le lancement se confirme sur une page du serveur.</small></div><Link className="btn btn-secondary btn-sm" to="/impressions/nouvelle">Préparer</Link></li>
          </ol>
        </section>
      ) : (
        <div className="dash-shortcuts" aria-label="Raccourcis">
          <Link className="btn btn-primary" to="/impressions/nouvelle"><Plus size={16} aria-hidden />Nouvelle impression</Link>
          <Link className="btn btn-secondary" to="/bibliotheque"><Upload size={16} aria-hidden />Importer un modèle</Link>
        </div>
      )}
      {conn.server === 'online' && !conn.importEnabled && conn.importNote && <p className="sub" style={{ marginTop: 'var(--sp-4)' }}>{conn.importNote}</p>}
    </>
  );
}
