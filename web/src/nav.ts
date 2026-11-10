/**
 * TABLE DE NAVIGATION UNIQUE de Printalk (G10 UX1).
 * Le menu latéral, la barre basse mobile, le fil d'Ariane, le titre d'onglet, les onglets (?vue=) et, plus tard,
 * la recherche globale lisent TOUS ce fichier. Un chantier qui ajoute un écran n'ajoute qu'une ligne ici (et sa <Route> dans main.tsx).
 * Règles : 7 entrées de premier niveau au plus, 4 onglets au plus par page, profondeur ≤ 3, l'ordre ne change jamais.
 */
import { Boxes, Disc3, FolderKanban, House, Printer, Settings, Wifi } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import { generatePath, matchPath } from 'react-router-dom';
import reserved from '../reserved-routes.json';

export interface NavEntry {
  id: string;
  /** Chemin absolu de l'entrée (« / » pour l'accueil). */
  path: string;
  label: string;
  icon: LucideIcon;
  /** Barre basse mobile (4 emplacements + « Plus ») ou feuille « Plus ». */
  mobile: 'bar' | 'more';
}

/** Les 7 entrées de premier niveau, dans l'ordre. Ne jamais en ajouter une 8e sans en retirer une autre. */
export const NAV: readonly NavEntry[] = [
  { id: 'accueil', path: '/', label: 'Accueil', icon: House, mobile: 'bar' },
  { id: 'projets', path: '/projets', label: 'Projets', icon: FolderKanban, mobile: 'bar' },
  { id: 'bibliotheque', path: '/bibliotheque', label: 'Bibliothèque', icon: Boxes, mobile: 'bar' },
  { id: 'impressions', path: '/impressions', label: 'Impressions', icon: Printer, mobile: 'bar' },
  { id: 'imprimantes', path: '/imprimantes', label: 'Imprimantes', icon: Wifi, mobile: 'more' },
  { id: 'matieres', path: '/matieres', label: 'Matières', icon: Disc3, mobile: 'more' },
  { id: 'reglages', path: '/reglages', label: 'Réglages', icon: Settings, mobile: 'more' },
];

export interface TabDecl { value: string; label: string }

export interface RouteDecl {
  /** Motif react-router absolu : « /modele/:id/preparer ». */
  path: string;
  /** Titre de page (onglet du navigateur, fil d'Ariane, recherche). */
  title: string;
  /** Motif du parent : obligatoire pour toute route qui n'est pas une entrée de premier niveau. */
  parent?: string;
  /** Mots-clés de recherche (U2). */
  keywords?: string[];
  /** Onglets de la page (≤ 4), état dans l'URL : ?vue=<value>. Le premier est le défaut. */
  tabs?: TabDecl[];
}

/** Tous les écrans de l'interface. Les écrans à paramètre (:id) sont atteints depuis leur parent, pas depuis le menu. */
export const ROUTES: readonly RouteDecl[] = [
  { path: '/', title: 'Accueil', keywords: ['tableau de bord', 'accueil'] },
  { path: '/projets', title: 'Projets', keywords: ['projet', 'assemblage', 'pièces'] },
  { path: '/bibliotheque', title: 'Bibliothèque', keywords: ['modèles', 'fichiers', 'importer', 'collections'] },
  { path: '/bibliotheque/gabarits', title: 'Pièces sur mesure', parent: '/bibliotheque', keywords: ['gabarits', 'paramétrique', 'pièce sur mesure'] },
  { path: '/bibliotheque/corbeille', title: 'Corbeille et doublons', parent: '/bibliotheque', keywords: ['corbeille', 'doublons', 'restaurer'],
    tabs: [{ value: 'corbeille', label: 'Corbeille' }, { value: 'doublons', label: 'Doublons exacts' }] },
  { path: '/modele/:id', title: 'Modèle', parent: '/bibliotheque' },
  { path: '/modele/:id/preparer', title: 'Préparer', parent: '/modele/:id', keywords: ['préparer', 'objectif', 'découpe'] },
  { path: '/modele/:id/essais', title: 'Essais de réglages', parent: '/modele/:id', keywords: ['essais', 'variantes', 'comparer'] },
  { path: '/modele/:id/essais/:exp', title: 'Essai de réglages', parent: '/modele/:id' },
  { path: '/impressions', title: 'Impressions', keywords: ['impressions', 'file', 'historique', 'bilan', 'coûts'],
    tabs: [{ value: 'a-faire', label: 'À faire' }, { value: 'en-cours', label: 'En cours et en file' }, { value: 'terminees', label: 'Terminées' }, { value: 'bilan', label: 'Bilan' }] },
  { path: '/impressions/nouvelle', title: 'Nouvelle impression', parent: '/impressions', keywords: ['nouvelle impression', 'préparer', 'découper'] },
  { path: '/imprimantes', title: 'Imprimantes', keywords: ['imprimantes', 'caméra', 'calibration', 'machines'],
    tabs: [{ value: 'mes', label: 'Mes imprimantes' }, { value: 'modeles', label: 'Modèles connus' }, { value: 'calibration', label: 'Calibration' }] },
  { path: '/matieres', title: 'Matières', keywords: ['bobines', 'ams', 'filament', 'matières', 'couleurs', 'fiches', 'catalogue Bambu', 'filaments Bambu'],
    tabs: [{ value: 'bobines', label: 'Bobines et AMS' }, { value: 'multicolore', label: 'Multicolore' }, { value: 'fiches', label: 'Fiches matières' }, { value: 'apres-impression', label: 'Après impression' }] },
  { path: '/reglages', title: 'Réglages', keywords: ['réglages', 'apparence', 'thème'] },
  { path: '/reglages/tarifs', title: 'Tarifs', parent: '/reglages', keywords: ['tarifs', 'prix', 'coût', 'électricité'] },
  { path: '/reglages/securite', title: 'Sécurité', parent: '/reglages', keywords: ['sécurité', 'passkey', 'code de lancement', 'notifications', 'enregistrer ce navigateur'] },
  { path: '/reglages/agent-windows', title: 'Agent Windows', parent: '/reglages', keywords: ['agent', 'windows', 'dossiers indexés'] },
  { path: '/reglages/avance', title: 'Avancé', parent: '/reglages', keywords: ['avancé', 'service', 'version', 'état'] },
  { path: '/reglages/avance/design', title: 'Système de design', parent: '/reglages/avance', keywords: ['design', 'jetons', 'composants'] },
  { path: '/dysfonctionnements', title: 'Dysfonctionnements', parent: '/reglages/avance', keywords: ['dysfonctionnements', 'carnet de bord', 'bugs'] },
];

/** Préfixes d'URL réservés au serveur (pages humaines, API, MCP) : aucune route ci-dessus ne peut y entrer. */
export const RESERVED_PREFIXES: readonly string[] = reserved.prefixes;
export const isReserved = (path: string) => RESERVED_PREFIXES.some((p) => path === p || path.startsWith(p + '/'));

/* ---------- lecture de la table ---------- */
const byPath = new Map(ROUTES.map((r) => [r.path, r] as const));
export const routeDecl = (path: string) => byPath.get(path);

/** Route déclarée qui correspond à une URL (sans requête), avec ses paramètres. */
export function matchRoute(pathname: string): { route: RouteDecl; params: Record<string, string | undefined> } | undefined {
  const clean = pathname.length > 1 ? pathname.replace(/\/+$/, '') : pathname;
  for (const route of ROUTES) {
    const m = matchPath({ path: route.path, end: true }, clean);
    if (m) return { route, params: m.params as Record<string, string | undefined> };
  }
  return undefined;
}

/** Chaîne de la route à son entrée de premier niveau (la route elle-même en dernier). */
export function chainOf(route: RouteDecl): RouteDecl[] {
  const out: RouteDecl[] = [route];
  let cur: RouteDecl | undefined = route;
  while (cur?.parent) { cur = byPath.get(cur.parent); if (cur) out.unshift(cur); else break; }
  return out;
}
/** Profondeur : 1 pour une entrée de premier niveau. */
export const depthOf = (route: RouteDecl) => chainOf(route).length;

export interface Crumb { label: string; to?: string }
/** Fil d'Ariane déduit de la table (les pages à noms propres, comme un modèle, peuvent le remplacer). */
export function crumbsFor(pathname: string): Crumb[] {
  const m = matchRoute(pathname);
  if (!m) return [];
  const chain = chainOf(m.route);
  return chain.map((r, i) => {
    const last = i === chain.length - 1;
    let to: string | undefined;
    if (!last) { try { to = generatePath(r.path, m.params as Record<string, string>); } catch { to = undefined; } }
    return { label: r.title, to };
  });
}

/** Entrée de premier niveau qui contient l'URL (pour la surbrillance du menu). */
export function entryFor(pathname: string): NavEntry | undefined {
  const m = matchRoute(pathname);
  if (!m) return undefined;
  const top = chainOf(m.route)[0];
  return NAV.find((n) => n.path === top.path);
}

/** Titre d'onglet du navigateur : « Bibliothèque », « Préparer », « Réglages ». Vide sur l'accueil. */
export function titleFor(pathname: string): string | undefined {
  const m = matchRoute(pathname);
  if (!m) return 'Introuvable';
  return m.route.path === '/' ? undefined : m.route.title;
}

/** Onglets d'une route (jusqu'à 4) ; vide si elle n'en a pas. */
export const tabsOf = (path: string): TabDecl[] => byPath.get(path)?.tabs ?? [];
