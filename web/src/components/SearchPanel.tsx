import { useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Box, Disc3, FileText, FolderKanban, Printer, Search, X } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import { api } from '../api/client';
import { insightsApi } from '../api/insights';
import { filamentApi } from '../api/filamentApi';
import { projectsApi } from '../api/projectsTypes';
import { ROUTES } from '../nav';
import './search.css';

/**
 * Recherche globale (G10 UX9 v1). Texte seulement : aucune interprétation de phrase, aucune IA. Sources : modèles (paramètre `q` du serveur),
 * projets, impressions du journal, imprimantes, bobines (listes déjà servies, filtrées côté client et bornées à 200 éléments : la mention
 * « résultats limités » apparaît quand la borne est atteinte) et pages de l'interface (table nav.ts). Tout texte affiché est échappé par React ;
 * la saisie n'est jamais insérée dans du HTML ni dans une URL non encodée. Ctrl/Cmd+K et « / » ouvrent le panneau ; Échap le ferme et rend le focus.
 */
export const SEARCH_CAP = 200;

type Group = 'Modèles' | 'Projets' | 'Impressions' | 'Imprimantes' | 'Matières' | 'Pages';
export const GROUPS: Group[] = ['Modèles', 'Projets', 'Impressions', 'Imprimantes', 'Matières', 'Pages'];
export interface Hit { group: Group; key: string; label: string; hint?: string; to: string }
const ICON: Record<Group, LucideIcon> = { Modèles: Box, Projets: FolderKanban, Impressions: Printer, Imprimantes: Printer, Matières: Disc3, Pages: FileText };

const norm = (s: string) => s.normalize('NFD').replace(/[̀-ͯ]/g, '').toLowerCase();
const has = (q: string, ...fields: Array<string | undefined>) => fields.some((f) => f && norm(f).includes(q));

/** Pages de l'interface qui correspondent (titre ou mots-clés ; les écrans à paramètre ne sont pas des destinations). */
export function pageHits(query: string): Hit[] {
  const q = norm(query.trim());
  if (!q) return [];
  return ROUTES.filter((r) => !r.path.includes(':') && (has(q, r.title) || (r.keywords ?? []).some((k) => norm(k).includes(q)))).slice(0, 6)
    .map((r) => ({ group: 'Pages', key: `p-${r.path}`, label: r.title, to: r.path }));
}

interface Loaded { projects?: Hit[]; prints?: Hit[]; printers?: Hit[]; spools?: Hit[]; capped: boolean }

/** Charge une fois les listes bornées (à l'ouverture du panneau) ; une source qui échoue est ignorée, jamais bloquante. */
function useSources(open: boolean): Loaded {
  const [d, setD] = useState<Loaded>({ capped: false });
  useEffect(() => {
    if (!open) return;
    let alive = true;
    const soft = <T,>(p: Promise<T>) => p.catch(() => undefined);
    void Promise.all([soft(projectsApi.list()), soft(insightsApi.log({ limit: SEARCH_CAP })), soft(api.printers()), soft(filamentApi.spools({}))]).then(([pr, lg, pn, sp]) => {
      if (!alive) return;
      const projects = pr?.projects.slice(0, SEARCH_CAP).map((i) => ({ group: 'Projets' as const, key: `j-${i.project.id}`, label: i.project.name, hint: i.progress.text, to: '/projets' }));
      const prints = lg?.entries.slice(0, SEARCH_CAP).map((e) => ({ group: 'Impressions' as const, key: `i-${e.print_job_id}`, label: e.name ?? e.model_name ?? e.print_job_id, hint: e.material_family, to: `/impressions/${encodeURIComponent(e.print_job_id)}` }));
      const printers = pn?.printers.map((p) => ({ group: 'Imprimantes' as const, key: `m-${p.id}`, label: p.name, hint: p.model_name, to: '/imprimantes' }));
      const spools = sp?.spools.slice(0, SEARCH_CAP).map((s) => ({ group: 'Matières' as const, key: `s-${s.id}`, label: s.name || `${s.brand ?? ''} ${s.material}`.trim(), hint: s.material, to: '/matieres' }));
      setD({ projects, prints, printers, spools, capped: (lg?.entries.length ?? 0) >= SEARCH_CAP || (sp?.spools.length ?? 0) >= SEARCH_CAP });
    });
    return () => { alive = false; };
  }, [open]);
  return d;
}

export function SearchPanel({ open, onClose }: { open: boolean; onClose: () => void }) {
  const nav = useNavigate();
  const ref = useRef<HTMLDialogElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const back = useRef<HTMLElement | null>(null);
  const [q, setQ] = useState('');
  const [debounced, setDebounced] = useState('');
  const [models, setModels] = useState<Hit[]>([]);
  const [modelsTotal, setModelsTotal] = useState(0);
  const [recent, setRecent] = useState<Hit[]>([]);
  const [active, setActive] = useState(0);
  const src = useSources(open);

  useEffect(() => {
    const d = ref.current; if (!d) return;
    if (open && !d.open) { back.current = document.activeElement as HTMLElement | null; d.showModal(); window.setTimeout(() => inputRef.current?.focus(), 0); }
    if (!open && d.open) { d.close(); back.current?.focus?.(); }
  }, [open]);
  useEffect(() => { if (!open) { setQ(''); setDebounced(''); setActive(0); } }, [open]);
  useEffect(() => { const t = window.setTimeout(() => setDebounced(q.trim()), 200); return () => window.clearTimeout(t); }, [q]);

  // Modèles : recherche par le serveur (paramètre `q`, encodé par le client d'API). Vide : les plus récemment modifiés.
  useEffect(() => {
    if (!open) return;
    const ac = new AbortController();
    api.models(debounced ? { q: debounced, limit: 8 } : { sort: 'updated', desc: true, limit: 5 }, ac.signal).then((r) => {
      const hits = r.models.map((m) => ({ group: 'Modèles' as const, key: `d-${m.id}`, label: m.name, hint: m.collection_path, to: `/modele/${encodeURIComponent(m.id)}` }));
      if (debounced) { setModels(hits); setModelsTotal(r.total); } else { setRecent(hits); setModels([]); setModelsTotal(0); }
    }, () => { if (!ac.signal.aborted) { setModels([]); setRecent([]); } });
    return () => ac.abort();
  }, [debounced, open]);

  const results = useMemo<Hit[]>(() => {
    const n = norm(debounced);
    if (!n) return [...recent, ...(src.prints ?? []).slice(0, 3)];
    const f = (l?: Hit[]) => (l ?? []).filter((h) => has(n, h.label, h.hint)).slice(0, 5);
    return [...models, ...f(src.projects), ...f(src.prints), ...f(src.printers), ...f(src.spools), ...pageHits(debounced)];
  }, [debounced, models, recent, src]);
  useEffect(() => setActive(0), [results.length, debounced]);

  const go = (h: Hit) => { onClose(); nav(h.to); };
  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === 'ArrowDown') { e.preventDefault(); setActive((a) => Math.min(results.length - 1, a + 1)); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); setActive((a) => Math.max(0, a - 1)); }
    else if (e.key === 'Enter' && results[active]) { e.preventDefault(); go(results[active]); }
  };
  const limited = debounced && (src.capped || modelsTotal > models.length);

  const groups = GROUPS.map((g) => ({ g, items: results.filter((h) => h.group === g) })).filter((x) => x.items.length > 0);
  let idx = -1;
  return (
    <dialog ref={ref} className="search-dlg" aria-label="Recherche" onCancel={(e) => { e.preventDefault(); onClose(); }} onClose={() => { if (open) onClose(); }}
      onClick={(e) => { if (e.target === ref.current) onClose(); }}>
      {open && (
        <div className="search-box">
          <div className="search-input">
            <Search size={16} aria-hidden />
            <input ref={inputRef} type="search" role="combobox" aria-expanded="true" aria-controls="search-results" aria-activedescendant={results[active] ? `sr-${results[active].key}` : undefined}
              aria-label="Rechercher un modèle, un projet, une impression, une imprimante, une matière ou une page" placeholder="Rechercher…" value={q} onChange={(e) => setQ(e.target.value)} onKeyDown={onKey} autoComplete="off" spellCheck={false} />
            <button type="button" className="icon-btn" aria-label="Fermer la recherche" onClick={onClose}><X size={16} aria-hidden /></button>
          </div>
          <div id="search-results" role="listbox" aria-label="Résultats" className="search-results">
            {!debounced && results.length > 0 && <p className="search-h">Récents</p>}
            {groups.map(({ g, items }) => {
              const I = ICON[g];
              return (
                <div key={g} role="group" aria-label={g}>
                  {debounced && <p className="search-h">{g}</p>}
                  {items.map((h) => {
                    idx++;
                    const i = idx;
                    return (
                      <button key={h.key} id={`sr-${h.key}`} type="button" role="option" aria-selected={i === active} className={`search-item${i === active ? ' on' : ''}`} onMouseEnter={() => setActive(i)} onClick={() => go(h)}>
                        <I size={15} aria-hidden /><span className="grow"><b>{h.label}</b>{h.hint && <small>{h.hint}</small>}</span>
                      </button>
                    );
                  })}
                </div>
              );
            })}
            {debounced && results.length === 0 && (
              <div className="search-empty" role="status">
                <p>Aucun résultat pour « {debounced} »</p>
                <p className="sub">Essayez un autre mot, ou :</p>
                <div className="search-sugg"><button type="button" className="btn btn-secondary btn-sm" onClick={() => { onClose(); nav('/bibliotheque'); }}>Importer un modèle</button><button type="button" className="btn btn-secondary btn-sm" onClick={() => { onClose(); nav('/impressions?vue=terminees'); }}>Voir toutes les impressions</button></div>
              </div>
            )}
            {limited && <p className="sub search-lim" role="note">Résultats limités : seuls les {SEARCH_CAP} éléments les plus récents de chaque liste sont parcourus.</p>}
          </div>
          <p className="sub search-foot">↑ ↓ pour choisir, Entrée pour ouvrir, Échap pour fermer.</p>
        </div>
      )}
    </dialog>
  );
}

/** Raccourcis : « / » (hors champ de saisie) et Ctrl/Cmd + K ouvrent la recherche. */
export function useSearchShortcut(open: () => void) {
  const ref = useRef(open); ref.current = open;
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement | null;
      const typing = !!t && (['INPUT', 'TEXTAREA', 'SELECT'].includes(t.tagName) || t.isContentEditable);
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); ref.current(); }
      else if (e.key === '/' && !typing && !e.ctrlKey && !e.metaKey && !e.altKey) { e.preventDefault(); ref.current(); }
    };
    window.addEventListener('keydown', h);
    return () => window.removeEventListener('keydown', h);
  }, []);
}
