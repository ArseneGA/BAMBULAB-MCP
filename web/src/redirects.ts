/**
 * Redirections des anciennes URL (G10 UX2, spec § 4.3). Toutes les anciennes routes restent valides indéfiniment :
 * liens enregistrés, courriels de notification et habitudes continuent de fonctionner. Les paramètres de la requête sont CONSERVÉS.
 * Table de données : le test (redirects.test.tsx) la parcourt ligne à ligne.
 */
export interface LegacyRule {
  /** Ancienne URL (chemin exact). */
  from: string;
  /** Exemple de requête de l'ancienne URL pour le test, et destination attendue (chemin + requête + ancre). */
  sample: { search: string; to: string };
  /** Destination (chemin + requête + ancre) ; `{ lookup }` quand la destination dépend d'une lecture serveur. */
  to: (sp: URLSearchParams) => string | { lookupExperiment: string };
  note?: string;
}

/** Requête sans les paramètres listés (les autres sont conservés tels quels), préfixée de « ? » ou vide. */
function rest(sp: URLSearchParams, drop: string[] = [], add: Record<string, string> = {}): string {
  const out = new URLSearchParams();
  for (const [k, v] of sp) if (!drop.includes(k)) out.append(k, v);
  for (const [k, v] of Object.entries(add)) out.set(k, v);
  const s = out.toString();
  return s ? `?${s}` : '';
}
const enc = encodeURIComponent;

export const LEGACY: readonly LegacyRule[] = [
  { from: '/modele', sample: { search: '', to: '/bibliotheque' }, to: (sp) => `/bibliotheque${rest(sp)}` },
  { from: '/corbeille', sample: { search: '?vue=doublons', to: '/bibliotheque/corbeille?vue=doublons' }, to: (sp) => `/bibliotheque/corbeille${rest(sp)}` },
  { from: '/gabarits', sample: { search: '', to: '/bibliotheque/gabarits' }, to: (sp) => `/bibliotheque/gabarits${rest(sp)}`, note: 'la calibration (ancien onglet, état local) est dans Imprimantes › Calibration' },
  { from: '/preparation', sample: { search: '?model=X1&objectif=2', to: '/modele/X1/preparer?objectif=2' }, to: (sp) => (sp.get('model') ? `/modele/${enc(sp.get('model')!)}/preparer${rest(sp, ['model'])}` : `/impressions/nouvelle${rest(sp)}`) },
  { from: '/comparaison', sample: { search: '?experiment=E9', to: '→ /modele/<modèle de l\'essai>/essais/E9' }, to: (sp) => (sp.get('experiment') ? { lookupExperiment: sp.get('experiment')! } : `/bibliotheque${rest(sp)}`) },
  { from: '/impression', sample: { search: '', to: '/impressions' }, to: (sp) => `/impressions${rest(sp)}` },
  { from: '/file', sample: { search: '', to: '/impressions?vue=en-cours' }, to: (sp) => `/impressions${rest(sp, ['vue'], { vue: 'en-cours' })}` },
  { from: '/bobines', sample: { search: '', to: '/matieres' }, to: (sp) => `/matieres${rest(sp)}` },
  { from: '/materiaux', sample: { search: '', to: '/matieres?vue=fiches' }, to: (sp) => `/matieres${rest(sp, ['vue'], { vue: 'fiches' })}`, note: 'les machines connues sont dans Imprimantes › Modèles connus' },
  { from: '/couts', sample: { search: '?onglet=journal', to: '/impressions?vue=terminees' }, note: 'une URL par onglet',
    to: (sp) => {
      const onglet = sp.get('onglet');
      if (onglet === 'journal') return `/impressions${rest(sp, ['onglet'], { vue: 'terminees' })}`;
      if (onglet === 'costs') return `/reglages/tarifs${rest(sp, ['onglet'])}`;
      if (onglet === 'post') return `/matieres${rest(sp, ['onglet'], { vue: 'apres-impression' })}`;
      if (onglet === 'dashboard') return `/impressions${rest(sp, ['onglet'], { vue: 'bilan' })}#usage`;
      return `/impressions${rest(sp, ['onglet'], { vue: 'bilan' })}`;
    } },
  { from: '/tableau-de-bord', sample: { search: '', to: '/impressions?vue=bilan' }, to: (sp) => `/impressions${rest(sp, ['vue'], { vue: 'bilan' })}` },
  { from: '/agents', sample: { search: '', to: '/reglages/agent-windows' }, to: (sp) => `/reglages/agent-windows${rest(sp)}` },
  { from: '/design', sample: { search: '', to: '/reglages/avance/design' }, to: (sp) => `/reglages/avance/design${rest(sp)}` },
];

/** URL qui ne changent pas (listées pour le test de la table § 4.3). */
export const UNCHANGED: readonly string[] = ['/', '/bibliotheque', '/modele/abc', '/impressions', '/imprimantes', '/reglages'];

export type LegacyTarget = { to: string } | { lookupExperiment: string; fallback: string };

/** Destination d'une ancienne URL, ou undefined si l'URL n'est pas une ancienne route. */
export function legacyTarget(pathname: string, search = ''): LegacyTarget | undefined {
  const clean = pathname.length > 1 ? pathname.replace(/\/+$/, '') : pathname;
  const rule = LEGACY.find((r) => r.from === clean);
  if (!rule) return undefined;
  const r = rule.to(new URLSearchParams(search));
  if (typeof r === 'string') return { to: r };
  return { lookupExperiment: r.lookupExperiment, fallback: '/bibliotheque' };
}
