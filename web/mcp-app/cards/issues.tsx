import type { CardProps } from './call';
import { fmtDate, isRec, num, str } from './format';
import { LiteBadge, LiteCard, LiteState, LiteStat, LiteStats, type Tone } from './lite';

/**
 * Carte Dysfonctionnements : list_issues (carnet de bord FB0). Lecture seule : aucun bouton, donc aucun geste d'impression ni de
 * changement d'état depuis la carte (les états se changent avec update_issue ou sur la page Réglages > Dysfonctionnements).
 */
export interface IssueRow {
  id: string; title: string; severity: 'P0' | 'P1' | 'P2' | 'P3'; tool?: string; expected?: string; observed?: string;
  print_job_id?: string; printer_id?: string; occurrences: number; regressions?: number; state: 'new' | 'seen' | 'fixed' | 'dismissed'; fix_commit?: string; last_seen_at?: string;
}
export interface IssuesData { issues: IssueRow[]; total: number; counts: Record<string, number> }

const SEV = ['P0', 'P1', 'P2', 'P3'];
const STATES = ['new', 'seen', 'fixed', 'dismissed'];

export function normalizeIssues(s: unknown): IssuesData | undefined {
  if (!isRec(s) || !Array.isArray(s.issues) || !isRec(s.counts)) return undefined;
  if (!s.issues.every((x) => isRec(x) && typeof x.id === 'string' && x.id.startsWith('iss_') && typeof x.title === 'string' && SEV.includes(String(x.severity)) && STATES.includes(String(x.state)))) return undefined;
  return { issues: s.issues as IssueRow[], total: num(s.total) ?? s.issues.length, counts: s.counts as Record<string, number> };
}

export const SEVERITY_TONE: Record<string, Tone> = { P0: 'fail', P1: 'fail', P2: 'warn', P3: 'off' };
export const SEVERITY_FR: Record<string, string> = { P0: 'P0 bloquant', P1: 'P1 grave', P2: 'P2 gênant', P3: 'P3 détail' };
export const STATE_FR: Record<string, string> = { new: 'nouveau', seen: 'vu', fixed: 'corrigé', dismissed: 'écarté' };
const STATE_TONE: Record<string, Tone> = { new: 'warn', seen: 'run', fixed: 'ok', dismissed: 'off' };

export function IssuesCard({ data }: CardProps<IssuesData>) {
  const c = data.counts;
  return (
    <LiteCard title="Dysfonctionnements" sub={`${data.total} constat(s) correspondant aux filtres`} label="Carnet de bord des dysfonctionnements">
      <LiteStats>
        <LiteStat label="Nouveaux" value={c.new ?? 0} tone={(c.new ?? 0) > 0 ? 'warn' : undefined} />
        <LiteStat label="Vus" value={c.seen ?? 0} />
        <LiteStat label="Corrigés" value={c.fixed ?? 0} />
        <LiteStat label="Écartés" value={c.dismissed ?? 0} />
      </LiteStats>
      {data.issues.length === 0
        ? <LiteState kind="empty">Aucun constat ne correspond.</LiteState>
        : (
          <ul className="lc-list" aria-label="Constats">
            {data.issues.map((i) => (
              <li key={i.id} className="lc-row" aria-label={`Constat ${i.title}`}>
                <LiteBadge tone={SEVERITY_TONE[i.severity]} title={SEVERITY_FR[i.severity]}>{i.severity}</LiteBadge>
                <span className="lc-row-main">
                  <span className="lc-row-name">{i.title}</span>
                  <span className="lc-row-meta">{i.tool ? `${i.tool} · ` : ''}{i.occurrences} occurrence{i.occurrences > 1 ? 's' : ''}{i.regressions ? ` · revenu ${i.regressions} fois après correction` : ''}{fmtDate(i.last_seen_at) ? ` · dernière : ${fmtDate(i.last_seen_at)}` : ''}</span>
                  {str(i.observed) && <span className="lc-row-meta">Observé : {i.observed}</span>}
                  {str(i.fix_commit) && <span className="lc-row-meta">Corrigé par le commit {i.fix_commit}</span>}
                </span>
                <span className="lc-row-end"><LiteBadge tone={STATE_TONE[i.state]}>{STATE_FR[i.state]}</LiteBadge></span>
              </li>
            ))}
          </ul>
        )}
      <p className="lc-note">Lecture seule. Pour tout coller à l'agent qui code : export_issues.</p>
    </LiteCard>
  );
}
