import { useEffect, useRef } from 'react';
import type { LogEntry } from '../../api/insights';
import type { PrintJob } from '../../api/printTypes';
import type { ListItem, PendingVerdict } from '../../api/projectsTypes';
import type { Spool } from '../../api/filamentApi';

/* Logique pure de l'Accueil (G10 UX6) : aucun appel réseau, tout est testable. Aucun calcul ne passe par une IA. */

/** Réglage « projet dormant » : nombre de jours sans activité (défaut 14), mémorisé dans ce navigateur. */
export const DORMANT_KEY = 'printalk.projet-dormant-jours';
export const DORMANT_DEFAULT = 14;
export function readDormantDays(): number {
  try { const n = Number(localStorage.getItem(DORMANT_KEY)); return Number.isFinite(n) && n > 0 ? Math.floor(n) : DORMANT_DEFAULT; } catch { return DORMANT_DEFAULT; }
}
export function writeDormantDays(n: number): void { try { localStorage.setItem(DORMANT_KEY, String(n)); } catch { /* stockage indisponible */ } }

/** Une impression du journal a atteint 100 % (D7) : « imprimée », verdict ou pas. Repli pour une fiche d'avant la migration. */
export const isPrinted = (e: LogEntry) => !!e.reached_100 || (e.state === 'COMPLETED' && e.outcome === 'success');

export interface MonthStats {
  /** Impressions du mois qui ont atteint 100 %. */
  printed: number;
  /** Parmi elles : jugées (réussie, ratée, à refaire), jugées réussies, non jugées. */
  judged: number; success: number; unjudged: number;
}
/** Taux de réussite honnête : « 7/8 jugées · 2 non jugées ». Les non jugées sont dites, jamais comptées dans le taux ; aucune division par zéro. */
export function monthStats(entries: LogEntry[], now = new Date()): MonthStats {
  const inMonth = entries.filter((e) => {
    const d = new Date(e.finished_at);
    return !Number.isNaN(d.getTime()) && d.getFullYear() === now.getFullYear() && d.getMonth() === now.getMonth();
  }).filter(isPrinted);
  let success = 0, judged = 0;
  for (const e of inMonth) {
    if (e.verdict === 'success') { success++; judged++; } else if (e.verdict === 'failed' || e.verdict === 'redo') judged++;
  }
  return { printed: inMonth.length, judged, success, unjudged: inMonth.length - judged };
}
export function judgedText(s: MonthStats): string {
  if (s.printed === 0) return 'aucune impression terminée ce mois-ci';
  const un = s.unjudged > 0 ? ` · ${s.unjudged} non jugée${s.unjudged > 1 ? 's' : ''}` : '';
  if (s.judged === 0) return `aucune jugée${un}`;
  return `${s.success}/${s.judged} jugée${s.judged > 1 ? 's' : ''}${un}`;
}

export type TodoKind = 'confirm' | 'alert' | 'verdict' | 'spool' | 'dormant';
export interface TodoItem {
  kind: TodoKind; key: string; title: string; detail: string;
  /** Lien : page du serveur (`server`, ouverte par un <a> ordinaire) ou route de l'interface. */
  href?: string; server?: boolean; verdict?: PendingVerdict; at?: string;
}
export const TODO_MAX = 5;

const DAY = 86_400_000;
export const ageText = (iso: string | undefined, now = Date.now()): string => {
  if (!iso) return '';
  const t = new Date(iso).getTime(); if (Number.isNaN(t)) return '';
  const d = Math.floor((now - t) / DAY);
  if (d <= 0) { const h = Math.floor((now - t) / 3_600_000); return h <= 0 ? 'à l\'instant' : `il y a ${h} h`; }
  return `il y a ${d} j`;
};
/** Heure locale « 14:32 » (indicateur « Mis à jour à… » des blocs en direct). */
export const fmtTime = (d?: Date) => (d ? d.toLocaleTimeString('fr-FR', { hour: '2-digit', minute: '2-digit' }) : '');
const dot = (a: string, b: string) => (b ? `${a} · ${b}` : a);

/** Projets racines non archivés, ni terminés ni abandonnés, sans activité depuis plus de `days` jours. */
export function dormantProjects(items: ListItem[], days: number, now = Date.now()): ListItem[] {
  return items.filter((i) => !i.project.parent_id && !i.project.archived && !['done', 'in_use', 'abandoned'].includes(i.project.status) && i.project.updated_at
    && now - new Date(i.project.updated_at).getTime() > days * DAY);
}

/** Bobines sous le seuil (« presque vide » ou « vide », calculé par le serveur), non archivées. */
export const lowSpools = (spools: Spool[]) => spools.filter((s) => !s.archived && (s.status === 'low' || s.status === 'empty'));

export interface TodoInput {
  jobs: PrintJob[]; pending: PendingVerdict[]; alerts: Array<{ job: PrintJob; summary: string; at?: string }>; spoolsNeeded: Array<{ name: string; spool: Spool }>; dormant: ListItem[]; now?: number;
}
/** Liste priorisée : à confirmer, alerte, verdict, bobine basse, projet dormant. */
export function buildTodo(i: TodoInput): TodoItem[] {
  const out: TodoItem[] = [];
  for (const j of i.jobs.filter((x) => ['UPLOADED', 'AWAITING_PRINT_CONFIRMATION'].includes(x.state))) {
    out.push({ kind: 'confirm', key: `c-${j.id}`, title: j.name ?? j.id, detail: dot('Prête à vérifier', ageText(j.updated_at, i.now)),
      href: j.confirmation_id ? `/confirm/${encodeURIComponent(j.confirmation_id)}` : `/impressions/${encodeURIComponent(j.id)}`, server: !!j.confirmation_id, at: j.updated_at });
  }
  for (const a of i.alerts) out.push({ kind: 'alert', key: `a-${a.job.id}`, title: a.job.name ?? a.job.id, detail: a.summary, href: `/impressions/${encodeURIComponent(a.job.id)}`, at: a.at });
  for (const p of i.pending) out.push({ kind: 'verdict', key: `v-${p.print_job_id}`, title: p.model_name || p.name || p.print_job_id, detail: dot('Terminée, pas encore jugée', ageText(p.finished_at, i.now)), verdict: p, at: p.finished_at });
  for (const s of i.spoolsNeeded) out.push({ kind: 'spool', key: `s-${s.spool.id}`, title: s.spool.name || s.spool.material, detail: `il reste ~${Math.round(s.spool.remaining_g)} g · requise par ${s.name}`, href: '/matieres' });
  for (const d of i.dormant) out.push({ kind: 'dormant', key: `d-${d.project.id}`, title: d.project.name, detail: ageText(d.project.updated_at, i.now).replace('il y a ', 'sans activité depuis '), href: '/projets' });
  return out;
}

/**
 * Sondage qui respecte Page Visibility : pause quand l'onglet est caché, une lecture immédiate au retour.
 * `run` n'est jamais appelé onglet caché.
 */
export function useVisiblePolling(run: () => void, everyMs: number, active = true): void {
  const ref = useRef(run); ref.current = run;
  useEffect(() => {
    if (!active) return;
    let timer: number | undefined;
    const visible = () => document.visibilityState !== 'hidden';
    const stop = () => { if (timer !== undefined) { window.clearInterval(timer); timer = undefined; } };
    const start = () => { if (timer === undefined) timer = window.setInterval(() => { if (visible()) ref.current(); }, everyMs); };
    const onVis = () => { if (visible()) { ref.current(); start(); } else stop(); };
    if (visible()) start();
    document.addEventListener('visibilitychange', onVis);
    return () => { stop(); document.removeEventListener('visibilitychange', onVis); };
  }, [everyMs, active]);
}

/** Titre d'onglet pendant une impression : « 42 % · Équerre · Printalk » (sans titre : « 42 % · Printalk »). */
export const printingTitle = (percent: number, name: string | undefined, product: string) => `${Math.round(percent)} % · ${name ? `${name} · ` : ''}${product}`;
