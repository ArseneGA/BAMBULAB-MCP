import { useToolCall, type CardProps } from './call';
import { fmtGrams, fmtMoney, fmtSeconds, isRec, num, str } from './format';
import { LiteBadge, LiteButton, LiteCard, LiteGauge, LiteState, LiteStat, LiteStats, type Tone } from './lite';

/**
 * Carte Projet (ui://bambu/project.html), liée à get_project (fiche) et list_projects (arbre). Les gestes passent par les outils
 * réservés à la carte : submit_project_status (statut) et submit_project_part (quantité d'une pièce). Aucun bouton de
 * lancement : ni cette carte ni ses outils ne démarrent, n'arrêtent ni ne reprennent d'impression.
 * « Imprimée » = l'impression a atteint 100 %, verdict ou pas ; le verdict (réussie, ratée, à refaire) s'affiche à part.
 */
export interface Progress {
  parts_done: number; parts_total: number; units_printed: number; units_total: number; units_judged_success: number; units_judged_failed: number;
  units_judged_redo: number; units_unjudged: number; complete: boolean; text: string;
}
export interface ProjectRow {
  id: string; name: string; description?: string; status: string; parent_id?: string; collection_id?: string; due_date?: string; archived?: boolean; rev?: number;
}
export interface PartRow {
  model_id: string; model_name?: string; quantity: number; role?: string; implicit?: boolean; trashed?: boolean; printed: number; judged_success: number;
  judged_failed: number; judged_redo: number; unjudged: number; attempts: number; in_progress: number; satisfied: boolean; text: string;
}
export interface SubRow { id: string; name: string; status: string; archived?: boolean; own: Progress; progress: Progress; parts_count: number; parent_id: string; depth: number }
export interface Totals {
  available: boolean; reason?: string; actual_cost: number; currency?: string; actual_seconds: number; actual_grams: number; planned_cost?: number; in_progress: number; attempts: number;
}
export interface ProjectDetail {
  project: ProjectRow; level: number; path?: Array<{ id: string; name: string }>; parts: PartRow[]; parts_truncated?: boolean; own: Progress; progress: Progress;
  sub_projects: SubRow[]; totals: Totals; variants: Array<{ id: string; name: string; goal_kind: string }>; suggested_status?: string; suggested_reason?: string; next_action: string;
  levels?: Record<string, string>; note?: string;
}
export interface ListRow { project: ProjectRow; depth: number; progress: Progress; parts_count: number; suggested_status?: string }
export interface ProjectList { projects: ListRow[]; total: number; counts_by_status: Record<string, number>; note?: string }
export type ProjectData = { kind: 'project'; detail: ProjectDetail } | { kind: 'list'; list: ProjectList };

const isPrj = (x: unknown): x is ProjectRow => isRec(x) && typeof x.id === 'string' && x.id.startsWith('prj_') && typeof x.name === 'string' && typeof x.status === 'string';

export function normalizeProject(s: unknown): ProjectData | undefined {
  if (!isRec(s)) return undefined;
  if (isPrj(s.project) && isRec(s.progress) && Array.isArray(s.parts) && Array.isArray(s.sub_projects)) return { kind: 'project', detail: s as unknown as ProjectDetail };
  if (Array.isArray(s.projects) && isRec(s.counts_by_status) && s.projects.every((r) => isRec(r) && isPrj(r.project) && isRec(r.progress))) {
    return { kind: 'list', list: { projects: s.projects as ListRow[], total: num(s.total) ?? s.projects.length, counts_by_status: s.counts_by_status as Record<string, number>, note: str(s.note) } };
  }
  return undefined;
}

export const STATUS_FR: Record<string, string> = { idea: 'Idée', design: 'Conception', printing: 'Impression', done: 'Terminé', in_use: 'En usage', abandoned: 'Abandonné' };
const STATUS_TONE: Record<string, Tone> = { idea: 'off', design: 'run', printing: 'run', done: 'ok', in_use: 'ok', abandoned: 'fail' };
const STATUSES = ['idea', 'design', 'printing', 'done', 'in_use', 'abandoned'];

const pct = (p: Progress): number | null => (p.units_total > 0 ? (p.units_printed / p.units_total) * 100 : null);

function Bar({ p, label }: { p: Progress; label: string }) {
  return (
    <div style={{ display: 'grid', gap: 4 }}>
      <LiteGauge value={pct(p)} tone={p.complete ? 'ok' : 'run'} label={label} />
      <span className="lc-note">{p.text}</span>
    </div>
  );
}

function SubTree({ subs, parent, onOpen, disabled }: { subs: SubRow[]; parent: string; onOpen: (id: string) => void; disabled: boolean }) {
  const kids = subs.filter((s) => s.parent_id === parent);
  if (kids.length === 0) return null;
  return (
    <ul className="lc-list" aria-label="Sous-projets" style={{ paddingLeft: parent ? 14 : 0 }}>
      {kids.map((s) => (
        <li key={s.id}>
          <details open>
            <summary aria-label={`Sous-projet ${s.name}`}>
              <span className="lc-row-name">{s.name}</span>{' '}
              <LiteBadge tone={STATUS_TONE[s.status] ?? 'off'}>{STATUS_FR[s.status] ?? s.status}</LiteBadge>{s.archived ? <> <LiteBadge>Archivé</LiteBadge></> : null}
              <span className="lc-row-meta"> · {s.progress.text}</span>
            </summary>
            <div style={{ display: 'grid', gap: 6, padding: '6px 0 6px 12px' }}>
              <Bar p={s.progress} label={`Avancement de ${s.name}`} />
              <span className="lc-note">Propre : {s.own.text} ({s.parts_count} pièce(s) directe(s)).</span>
              <div><LiteButton disabled={disabled} onClick={() => onOpen(s.id)}>Ouvrir ce sous-projet</LiteButton></div>
              <SubTree subs={subs} parent={s.id} onOpen={onOpen} disabled={disabled} />
            </div>
          </details>
        </li>
      ))}
    </ul>
  );
}

export function ProjectCard({ app, data, onOpen }: CardProps<ProjectData>) {
  const { busy, error, run } = useToolCall(app, onOpen);
  const dis = busy !== null;

  if (data.kind === 'list') {
    const l = data.list;
    const open = (id: string) => { void run(id, 'get_project', { id }); };
    return (
      <LiteCard title="Projets" sub={l.note ?? `${l.total} projet(s)`} label="Liste des projets"
        aside={Object.entries(l.counts_by_status).filter(([, n]) => n > 0).map(([k, n]) => <LiteBadge key={k} tone={STATUS_TONE[k] ?? 'off'}>{STATUS_FR[k] ?? k} {n}</LiteBadge>)}>
        {l.projects.length === 0
          ? <LiteState kind="empty">Aucun projet : en créer un avec create_project, puis lui ajouter des pièces.</LiteState>
          : (
            <ul className="lc-list" aria-label="Projets">
              {l.projects.map((r) => (
                <li key={r.project.id} style={{ paddingLeft: r.depth * 16 }}>
                  <button type="button" className="lc-row" disabled={dis} aria-busy={busy === r.project.id} aria-label={`Projet ${r.project.name}`} onClick={() => open(r.project.id)}>
                    <span className="lc-row-main">
                      <span className="lc-row-name">{r.project.name}</span>
                      <span className="lc-row-meta">{r.progress.text}{r.suggested_status ? ` · statut suggéré : ${STATUS_FR[r.suggested_status] ?? r.suggested_status}` : ''}</span>
                      <LiteGauge value={pct(r.progress)} tone={r.progress.complete ? 'ok' : 'run'} label={`Avancement de ${r.project.name}`} height={6} />
                    </span>
                    <span className="lc-row-end"><LiteBadge tone={STATUS_TONE[r.project.status] ?? 'off'}>{STATUS_FR[r.project.status] ?? r.project.status}</LiteBadge>{r.project.archived ? <LiteBadge>Archivé</LiteBadge> : null}</span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        {error && <LiteState kind="error">{error}</LiteState>}
        <p className="lc-note">Choisir un projet ouvre sa fiche. Les projets ne lancent aucune impression.</p>
      </LiteCard>
    );
  }

  const d = data.detail;
  const p = d.project;
  const t = d.totals;
  const setStatus = (status: string) => { void run(`s-${status}`, 'submit_project_status', { project_id: p.id, status }); };
  const setQty = (m: PartRow, q: number) => { void run(`q-${m.model_id}`, 'submit_project_part', { project_id: p.id, model_id: m.model_id, quantity: q }); };
  const openPart = (id: string) => { void run(`o-${id}`, 'get_model_details', { model_id: id }); };
  const openProject = (id: string) => { void run(id, 'get_project', { id }); };
  const path = (d.path ?? []).map((x) => x.name).join(' › ');
  return (
    <LiteCard title={p.name} sub={[path, p.due_date ? `échéance ${p.due_date}` : '', p.description].filter(Boolean).join(' · ')} label={`Projet ${p.name}`}
      aside={<><LiteBadge tone={STATUS_TONE[p.status] ?? 'off'}>{STATUS_FR[p.status] ?? p.status}</LiteBadge>{p.archived ? <LiteBadge>Archivé</LiteBadge> : null}</>}>
      <Bar p={d.progress} label={`Avancement du projet ${p.name}`} />
      <LiteStats>
        <LiteStat label="Pièces" value={`${d.progress.parts_done}/${d.progress.parts_total}`} hint="imprimées (100 % atteint)" />
        <LiteStat label="Coût réel" value={t.available ? fmtMoney(t.actual_cost, t.currency || 'EUR') ?? 'non disponible' : 'non disponible'} hint={t.available ? 'calculé' : t.reason}
          tone={undefined} />
        <LiteStat label="Temps réel" value={t.available ? fmtSeconds(t.actual_seconds) ?? 'non disponible' : 'non disponible'} hint={t.available ? `mesuré · ${fmtGrams(t.actual_grams) ?? ''}` : undefined} />
      </LiteStats>
      {(t.in_progress ?? 0) > 0 && <p className="lc-note">{t.in_progress} tentative(s) en cours, coût estimé à part : {fmtMoney(t.planned_cost ?? 0, t.currency || 'EUR')}.</p>}
      <p className="lc-note">Jugement : {d.progress.units_judged_success} réussie(s), {d.progress.units_judged_failed + d.progress.units_judged_redo} ratée(s) ou à refaire, {d.progress.units_unjudged} non jugée(s) (déclaré).</p>

      <div role="group" aria-label="Statut du projet" style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
        {STATUSES.map((s) => <LiteButton key={s} pressed={p.status === s} busy={busy === `s-${s}`} disabled={dis || p.status === s} onClick={() => setStatus(s)}>{STATUS_FR[s]}</LiteButton>)}
      </div>
      {d.suggested_status && (
        <p className="lc-note" role="status">Statut suggéré : {STATUS_FR[d.suggested_status] ?? d.suggested_status} ({d.suggested_reason}). Rien ne change sans votre geste.{' '}
          <LiteButton disabled={dis} onClick={() => setStatus(d.suggested_status!)}>Passer en « {STATUS_FR[d.suggested_status] ?? d.suggested_status} »</LiteButton></p>
      )}

      <h2 className="lc-h2">Pièces</h2>
      {d.parts.length === 0
        ? <LiteState kind="empty">Aucune pièce : en ajouter avec add_project_part.</LiteState>
        : (
          <ul className="lc-list" aria-label="Pièces du projet">
            {d.parts.map((m) => (
              <li key={m.model_id} className="lc-row" aria-label={`Pièce ${m.model_name || m.model_id}`}>
                <button type="button" className="lc-row-main" style={{ all: 'unset', cursor: 'pointer', display: 'grid', gap: 2 }} disabled={dis} aria-label={`Ouvrir la pièce ${m.model_name || m.model_id}`} onClick={() => openPart(m.model_id)}>
                  <span className="lc-row-name">{m.model_name || m.model_id}{m.role ? ` · ${m.role}` : ''}{m.trashed ? ' (corbeille)' : ''}</span>
                  <span className="lc-row-meta">{m.text}</span>
                </button>
                <span className="lc-row-end">
                  <LiteBadge tone={m.satisfied ? 'ok' : m.in_progress > 0 ? 'run' : 'off'}>{m.satisfied ? 'Imprimée' : m.in_progress > 0 ? 'En cours' : 'À imprimer'}</LiteBadge>
                  {m.judged_failed + m.judged_redo > 0 && <LiteBadge tone="warn">Jugée ratée ou à refaire ×{m.judged_failed + m.judged_redo}</LiteBadge>}
                  {!m.implicit && (
                    <span style={{ display: 'inline-flex', gap: 4, alignItems: 'center' }}>
                      <LiteButton disabled={dis || m.quantity <= 1} title="Quantité voulue" onClick={() => setQty(m, m.quantity - 1)}>− quantité</LiteButton>
                      <span aria-label={`Quantité voulue ${m.quantity}`}>×{m.quantity}</span>
                      <LiteButton disabled={dis || m.quantity >= 1000} onClick={() => setQty(m, m.quantity + 1)}>+ quantité</LiteButton>
                    </span>
                  )}
                  {m.implicit && <span className="lc-dim">×1 (collection adoptée)</span>}
                </span>
              </li>
            ))}
          </ul>
        )}
      {d.parts_truncated && <p className="lc-note">Liste tronquée à 500 pièces.</p>}

      {d.sub_projects.length > 0 && (
        <>
          <h2 className="lc-h2">Sous-projets</h2>
          <SubTree subs={d.sub_projects} parent={p.id} onOpen={openProject} disabled={dis} />
        </>
      )}
      {d.variants.length > 0 && (
        <>
          <h2 className="lc-h2">Déclinaisons</h2>
          <ul className="lc-list" aria-label="Déclinaisons">{d.variants.map((v) => <li key={v.id} className="lc-row"><span className="lc-row-name">{v.name}</span><LiteBadge>{v.goal_kind}</LiteBadge></li>)}</ul>
        </>
      )}
      <p className="lc-note">Prochaine étape : {d.next_action}</p>
      {error && <LiteState kind="error">{error}</LiteState>}
    </LiteCard>
  );
}
