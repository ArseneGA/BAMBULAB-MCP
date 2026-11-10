// @vitest-environment jsdom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import type { Root } from 'react-dom/client';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { LEGACY, UNCHANGED, legacyTarget } from './redirects';
import { LegacyRedirect } from './pages/LegacyRedirects';
import { NotFoundPage } from './pages/NotFound';
import { NAV } from './nav';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLElement; let root: Root;
beforeEach(() => { host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host); });
afterEach(() => { act(() => root.unmount()); host.remove(); vi.unstubAllGlobals(); });

/** Routeur en mémoire avec les mêmes routes de redirection que main.tsx ; affiche l'URL finale. */
function Probe() { const l = useLocation(); return <output data-testid="url">{l.pathname + l.search + l.hash}</output>; }
let visits = 0;
async function visit(url: string): Promise<string> {
  await act(async () => {
    root.render(
      <MemoryRouter key={++visits} initialEntries={[url]}>
        <Routes>
          <Route index element={<Probe />} />
          <Route path="bibliotheque" element={<Probe />} />
          <Route path="modele/:id/*" element={<Probe />} />
          {LEGACY.map((r) => <Route key={r.from} path={r.from.slice(1)} element={<LegacyRedirect />} />)}
          <Route path="impressions/*" element={<Probe />} />
          <Route path="matieres" element={<Probe />} />
          <Route path="reglages/*" element={<Probe />} />
          <Route path="bibliotheque/*" element={<Probe />} />
          <Route path="*" element={<><Probe /><NotFoundPage /></>} />
        </Routes>
      </MemoryRouter>,
    );
  });
  await act(async () => { await Promise.resolve(); });
  return host.querySelector('[data-testid="url"]')!.textContent!;
}

describe('anciennes URL : table § 4.3 (CA-N2)', () => {
  for (const r of LEGACY.filter((x) => !x.sample.to.startsWith('→'))) {
    it(`${r.from}${r.sample.search} → ${r.sample.to}`, async () => {
      expect(legacyTarget(r.from, r.sample.search)).toEqual({ to: r.sample.to });
      expect(await visit(r.from + r.sample.search)).toBe(r.sample.to);
    });
  }

  it('conserve les paramètres de la requête (exemples de la spec)', async () => {
    expect(await visit('/couts?onglet=journal')).toBe('/impressions?vue=terminees');
    expect(await visit('/preparation?model=X')).toBe('/modele/X/preparer');
    expect(await visit('/bibliotheque?q=%C3%A9querre&f=stl')).toBe('/bibliotheque?q=%C3%A9querre&f=stl'); // inchangée
    expect(await visit('/preparation?model=X&objectif=2')).toBe('/modele/X/preparer?objectif=2');
    expect(await visit('/corbeille?vue=doublons')).toBe('/bibliotheque/corbeille?vue=doublons');
  });

  it('toutes les variantes de /couts?onglet=…', () => {
    const t = (s: string) => (legacyTarget('/couts', s) as { to: string }).to;
    expect(t('')).toBe('/impressions?vue=bilan');
    expect(t('?onglet=overview')).toBe('/impressions?vue=bilan');
    expect(t('?onglet=dashboard')).toBe('/impressions?vue=bilan#usage');
    expect(t('?onglet=journal')).toBe('/impressions?vue=terminees');
    expect(t('?onglet=costs')).toBe('/reglages/tarifs');
    expect(t('?onglet=post')).toBe('/matieres?vue=apres-impression');
    expect(t('?onglet=journal&x=1')).toBe('/impressions?x=1&vue=terminees');
  });

  it('/preparation sans modèle → assistant « Nouvelle impression » ; /comparaison sans essai → bibliothèque', () => {
    expect(legacyTarget('/preparation', '')).toEqual({ to: '/impressions/nouvelle' });
    expect(legacyTarget('/comparaison', '')).toEqual({ to: '/bibliotheque' });
  });

  it('/comparaison?experiment=ID lit l\'essai puis redirige vers /modele/<id>/essais/ID (repli : bibliothèque)', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ id: 'E9', model_id: 'M7' }), { status: 200, headers: { 'Content-Type': 'application/json' } })));
    expect(await visit('/comparaison?experiment=E9')).toBe('/modele/M7/essais/E9');
    vi.stubGlobal('fetch', vi.fn(async () => new Response('{"error":"absent"}', { status: 404, headers: { 'Content-Type': 'application/json' } })));
    await act(async () => { root.unmount(); });
    root = createRoot(host);
    expect(await visit('/comparaison?experiment=E9')).toBe('/bibliotheque');
  });

  it('les URL inchangées ne sont pas redirigées', async () => {
    for (const u of UNCHANGED) expect(legacyTarget(u)).toBeUndefined();
  });
});

describe('page « Introuvable » (CA-N5)', () => {
  it('une route inconnue reste à son adresse, affiche un h1, une recherche et les 7 liens du menu', async () => {
    expect(await visit('/nimporte/quoi')).toBe('/nimporte/quoi');
    expect(host.querySelectorAll('h1')).toHaveLength(1);
    expect(host.querySelector('h1')!.textContent).toMatch(/introuvable/i);
    expect(host.querySelector('[role="search"] input')).not.toBeNull();
    const links = [...host.querySelectorAll('ul[aria-label="Sections de Printalk"] a')].map((a) => a.textContent);
    expect(links).toEqual(NAV.map((n) => n.label));
  });
});
