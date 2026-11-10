import { describe, expect, it } from 'vitest';
import { NAV, RESERVED_PREFIXES, ROUTES, chainOf, crumbsFor, depthOf, entryFor, isReserved, matchRoute, tabsOf, titleFor } from './nav';
import { LEGACY, UNCHANGED } from './redirects';
import mainSource from './main.tsx?raw';

describe('table de navigation (CA-N1)', () => {
  it('déclare exactement 7 entrées de premier niveau, dans l\'ordre', () => {
    expect(NAV.map((n) => n.label)).toEqual(['Accueil', 'Projets', 'Bibliothèque', 'Impressions', 'Imprimantes', 'Matières', 'Réglages']);
    expect(NAV.map((n) => n.path)).toEqual(['/', '/projets', '/bibliotheque', '/impressions', '/imprimantes', '/matieres', '/reglages']);
  });
  it('mobile : 4 entrées dans la barre basse (+ « Plus »), les 3 autres dans la feuille', () => {
    expect(NAV.filter((n) => n.mobile === 'bar').map((n) => n.label)).toEqual(['Accueil', 'Projets', 'Bibliothèque', 'Impressions']);
    expect(NAV.filter((n) => n.mobile === 'more').map((n) => n.label)).toEqual(['Imprimantes', 'Matières', 'Réglages']);
  });
});

describe('graphe de navigation (CA-N3)', () => {
  const top = new Set(NAV.map((n) => n.path));
  it('toute route déclarée a un parent déclaré et remonte à une entrée du menu', () => {
    for (const r of ROUTES) {
      if (top.has(r.path)) { expect(r.parent).toBeUndefined(); continue; }
      expect(r.parent, `${r.path} sans parent`).toBeDefined();
      expect(ROUTES.some((p) => p.path === r.parent), `${r.path} : parent ${r.parent} non déclaré`).toBe(true);
      expect(top.has(chainOf(r)[0].path), `${r.path} ne remonte pas à une entrée du menu`).toBe(true);
    }
  });
  it('profondeur ≤ 3 ; les écrans statiques courants sont à 2 clics du menu', () => {
    for (const r of ROUTES) expect(depthOf(r), r.path).toBeLessThanOrEqual(3);
    for (const p of ['/bibliotheque/corbeille', '/bibliotheque/gabarits', '/impressions/nouvelle', '/reglages/tarifs', '/reglages/securite', '/reglages/agent-windows', '/reglages/avance']) {
      expect(depthOf(ROUTES.find((r) => r.path === p)!), p).toBe(2);
    }
  });
  it('Comparaison, File, Agents et Design ne sont plus hors menu', () => {
    for (const p of ['/modele/:id/essais', '/reglages/agent-windows', '/reglages/avance/design']) expect(ROUTES.some((r) => r.path === p), p).toBe(true);
    expect(chainOf(ROUTES.find((r) => r.path === '/reglages/avance/design')!).map((r) => r.path)).toEqual(['/reglages', '/reglages/avance', '/reglages/avance/design']);
  });
  it('chaque <Route> de main.tsx est déclarée dans nav.ts ou dans la table des anciennes URL', () => {
    const declared = new Set(ROUTES.map((r) => r.path.replace(/^\//, '')));
    const legacy = new Set(LEGACY.map((r) => r.from.replace(/^\//, '')));
    const used = [...mainSource.matchAll(/<Route\s+path="([^"]*)"/g)].map((m) => m[1]).filter((p) => p !== '*');
    expect(used.length).toBeGreaterThan(15);
    for (const p of used) expect(declared.has(p) || legacy.has(p) || p.startsWith('{'), `route ${p} non déclarée`).toBe(true);
    for (const p of declared) if (p !== '') expect(used, `route ${p} non branchée dans main.tsx`).toContain(p);
  });
});

describe('non-collision avec le serveur (CA-N4)', () => {
  it('aucune route SPA ni ancienne URL ne commence par un préfixe réservé', () => {
    for (const must of ['/setup', '/confirm', '/stop', '/action', '/enroll', '/oauth', '/api', '/mcp', '/health', '/agents/pair']) expect(RESERVED_PREFIXES).toContain(must);
    for (const r of ROUTES) expect(isReserved(r.path), r.path).toBe(false);
    for (const r of LEGACY) expect(isReserved(r.from), r.from).toBe(false);
  });
  it('le test de préfixe respecte la frontière de segment', () => {
    expect(isReserved('/api/x')).toBe(true);
    expect(isReserved('/apiary')).toBe(false);
    expect(isReserved('/agents')).toBe(false); // l'ancienne URL /agents est une route SPA ; seul /agents/pair est au serveur
    expect(isReserved('/agents/pair')).toBe(true);
  });
});

describe('onglets (CA-N8)', () => {
  it('au plus 4 onglets par page, valeurs uniques, tous portés par ?vue=', () => {
    for (const r of ROUTES) {
      const t = tabsOf(r.path);
      expect(t.length, r.path).toBeLessThanOrEqual(4);
      expect(new Set(t.map((x) => x.value)).size).toBe(t.length);
    }
    expect(tabsOf('/impressions').map((t) => t.value)).toEqual(['a-faire', 'en-cours', 'terminees', 'bilan']);
    expect(tabsOf('/matieres').map((t) => t.value)).toEqual(['bobines', 'multicolore', 'fiches', 'apres-impression']);
  });
});

describe('lecture de la table', () => {
  it('fil d\'Ariane, titre et entrée active se déduisent de nav.ts', () => {
    expect(crumbsFor('/modele/abc/preparer').map((c) => [c.label, c.to])).toEqual([['Bibliothèque', '/bibliotheque'], ['Modèle', '/modele/abc'], ['Préparer', undefined]]);
    expect(crumbsFor('/')).toHaveLength(1);
    expect(titleFor('/matieres')).toBe('Matières');
    expect(titleFor('/')).toBeUndefined();
    expect(titleFor('/nimporte/quoi')).toBe('Introuvable');
    expect(entryFor('/reglages/avance/design')?.label).toBe('Réglages');
    expect(matchRoute('/modele/abc/essais/e1')?.params).toMatchObject({ id: 'abc', exp: 'e1' });
  });
  it('la table des anciennes URL couvre toutes les anciennes routes de la spec § 4.3', () => {
    const froms = LEGACY.map((r) => r.from);
    for (const f of ['/modele', '/corbeille', '/gabarits', '/preparation', '/comparaison', '/impression', '/file', '/bobines', '/materiaux', '/couts', '/tableau-de-bord', '/agents', '/design']) expect(froms).toContain(f);
    expect(UNCHANGED).toContain('/bibliotheque');
  });
});
