import { ApiError, BASE, CSRF_HEADER } from './client';

/* Carnet de bord des dysfonctionnements (routes /api/issues, internal/httpapi/issues.go). Aucune route ne touche aux impressions. */

export type IssueSeverity = 'P0' | 'P1' | 'P2' | 'P3';
export type IssueState = 'new' | 'seen' | 'fixed' | 'dismissed';

export interface Issue {
  id: string; title: string; severity: IssueSeverity; tool?: string; expected?: string; observed?: string;
  print_job_id?: string; printer_id?: string; model_id?: string; occurrences: number; regressions?: number;
  state: IssueState; fix_commit?: string; created_at: string; last_seen_at: string; updated_at: string;
}
export interface IssueList { issues: Issue[]; total: number; counts: Record<IssueState, number> }
export interface IssueQuery { state?: string; severity?: string; tool?: string; limit?: number; offset?: number }
export interface IssueUpdate { state?: IssueState; fix_commit?: string; severity?: IssueSeverity }

export const SEVERITY_LABEL: Record<IssueSeverity, string> = { P0: 'P0 · bloquant', P1: 'P1 · grave', P2: 'P2 · gênant', P3: 'P3 · détail' };

async function req<T>(method: string, path: string, body?: unknown, signal?: AbortSignal, asText = false): Promise<T> {
  let res: Response;
  try {
    res = await fetch(BASE + path, {
      method, signal, credentials: 'same-origin',
      headers: { ...CSRF_HEADER, ...(body === undefined ? {} : { 'Content-Type': 'application/json' }) },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch (e) {
    if ((e as Error).name === 'AbortError') throw e;
    throw new ApiError('Printalk est injoignable', 0, 'offline', true);
  }
  const text = await res.text();
  if (res.ok && asText) return text as T;
  let data: unknown;
  if (text) { try { data = JSON.parse(text); } catch { /* corps non JSON */ } }
  if (!res.ok) {
    const d = data as { error?: string; code?: string } | undefined;
    const gateway = d?.error === undefined && [502, 503, 504].includes(res.status);
    throw new ApiError(d?.error ?? `Erreur HTTP ${res.status}`, res.status, d?.code ?? (gateway ? 'offline' : 'http'), gateway);
  }
  return data as T;
}

const qs = (p: Record<string, string | number | undefined>) => {
  const u = new URLSearchParams();
  for (const [k, v] of Object.entries(p)) if (v !== undefined && v !== '') u.set(k, String(v));
  const s = u.toString();
  return s ? `?${s}` : '';
};

export const issuesApi = {
  list: (p: IssueQuery, signal?: AbortSignal) => req<IssueList>('GET', '/api/issues' + qs({ ...p }), undefined, signal),
  update: (id: string, u: IssueUpdate) => req<Issue>('PATCH', `/api/issues/${encodeURIComponent(id)}`, u),
  /** Markdown à coller à l'agent qui code. */
  exportMarkdown: (p: Pick<IssueQuery, 'state' | 'severity' | 'tool'>, signal?: AbortSignal) => req<string>('GET', '/api/issues/export' + qs({ ...p }), undefined, signal, true),
};
