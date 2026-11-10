// @vitest-environment jsdom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import type { Root } from 'react-dom/client';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Shell, MenuChangedBanner } from './Shell';
import { Breadcrumb, MobileTabBar, PageHeader } from './components';
import { NavProgress } from './components';
import { useTab } from './tabs';
import { Tabs } from '@shared/components';
import { NAV } from './nav';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
window.scrollTo = (() => undefined) as typeof window.scrollTo;
window.matchMedia = ((q: string) => ({ matches: false, media: q, addEventListener: () => undefined, removeEventListener: () => undefined, addListener: () => undefined, removeListener: () => undefined, onchange: null, dispatchEvent: () => false })) as unknown as typeof window.matchMedia;

let host: HTMLElement; let root: Root;
beforeEach(() => {
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  vi.stubGlobal('fetch', vi.fn(async () => new Response('{}', { status: 404, headers: { 'Content-Type': 'application/json' } })));
  const mem = new Map<string, string>(); // stockage de test (le localStorage de l'environnement peut manquer ou être partagé)
  vi.stubGlobal('localStorage', { getItem: (k: string) => mem.get(k) ?? null, setItem: (k: string, v: string) => { mem.set(k, v); }, removeItem: (k: string) => { mem.delete(k); }, clear: () => mem.clear() });
});
afterEach(() => { act(() => root.unmount()); host.remove(); vi.unstubAllGlobals(); });
const flush = async () => { await act(async () => { await Promise.resolve(); await new Promise((r) => setTimeout(r, 0)); }); };

describe('menu rendu (CA-N1)', () => {
  it('la barre latérale et la barre basse reflètent nav.ts : 7 entrées / 4 entrées + « Plus »', async () => {
    await act(async () => {
      root.render(<MemoryRouter initialEntries={['/matieres']}><Routes><Route element={<Shell />}><Route path="matieres" element={<PageHeader title="Matières" />} /></Route></Routes></MemoryRouter>);
    });
    await flush();
    const side = [...host.querySelectorAll('aside nav a')].map((a) => a.querySelector('.t')?.textContent);
    expect(side).toEqual(NAV.map((n) => n.label));
    const tab = [...host.querySelectorAll('nav.tabbar > *')].map((e) => e.textContent?.replace(/\d+$/, ''));
    expect(tab).toEqual(['Accueil', 'Projets', 'Bibliothèque', 'Impressions', 'Plus']);
    // l'entrée courante est marquée
    expect(host.querySelector('aside nav a.active .t')?.textContent).toBe('Matières');
    // un seul h1, repère principal étiqueté
    expect(host.querySelectorAll('h1')).toHaveLength(1);
    expect(host.querySelector('main')?.getAttribute('aria-label')).toBeTruthy();
  });
});

describe('barre basse mobile (CA-N6)', () => {
  it('libellés visibles sur 5 emplacements ; la feuille « Plus » s\'ouvre, liste les 3 autres entrées, et rend le focus à sa fermeture', async () => {
    await act(async () => { root.render(<MemoryRouter><MobileTabBar badges={{ impressions: 2 }} /></MemoryRouter>); });
    const items = host.querySelectorAll('nav.tabbar > *');
    expect(items).toHaveLength(5);
    for (const it of items) expect(it.querySelector('.tb-t')?.textContent?.length).toBeGreaterThan(0);
    expect(host.querySelector('.tb-n')?.textContent).toBe('2');
    const plus = host.querySelector<HTMLButtonElement>('nav.tabbar button')!;
    expect(plus.getAttribute('aria-haspopup')).toBe('dialog');
    await act(async () => { plus.focus(); plus.click(); });
    const sheet = host.querySelector('dialog.sheet')!;
    expect(sheet.hasAttribute('open')).toBe(true);
    expect([...sheet.querySelectorAll('.sheet-list a')].map((a) => a.textContent)).toEqual(['Imprimantes', 'Matières', 'Réglages']);
    await act(async () => { sheet.querySelector<HTMLButtonElement>('button[aria-label="Fermer"]')!.click(); });
    expect(sheet.hasAttribute('open')).toBe(false);
    expect(document.activeElement).toBe(plus);
  });
  // Les règles de feuille de style (cibles ≥ 44 px, 5 colonnes, marge basse, prefers-reduced-motion, lien « ← Parent ») sont vérifiées
  // par le test Go internal/webui/css_test.go, qui lit web/src/shell.css (vitest neutralise les feuilles de style).
});

describe('fil d\'Ariane (CA-N7)', () => {
  it('visible dès la profondeur 2, dernier élément sans lien (aria-current), lien « ← Parent » pour le mobile', async () => {
    await act(async () => { root.render(<MemoryRouter initialEntries={['/modele/abc/preparer']}><Breadcrumb /></MemoryRouter>); });
    const crumb = host.querySelector('nav[aria-label="Fil d\'Ariane"]')!;
    const items = [...crumb.querySelectorAll('ol li')];
    expect(items.map((li) => li.textContent?.replace('›', ''))).toEqual(['Bibliothèque', 'Modèle', 'Préparer']);
    const last = items[2].querySelector('[aria-current="page"]');
    expect(last?.tagName).toBe('SPAN');
    expect(items[0].querySelector('a')?.getAttribute('href')).toBe('/bibliotheque');
    const back = crumb.querySelector('a.crumb-back');
    expect(back?.getAttribute('href')).toBe('/modele/abc');
  });
  it('absent au premier niveau ; remplaçable par la page (noms propres)', async () => {
    await act(async () => { root.render(<MemoryRouter initialEntries={['/impressions']}><Breadcrumb /></MemoryRouter>); });
    expect(host.querySelector('nav.crumb')).toBeNull();
    await act(async () => { root.render(<MemoryRouter initialEntries={['/impressions']}><Breadcrumb items={[{ label: 'Bibliothèque', to: '/bibliotheque' }, { label: 'Atelier' }, { label: 'Support' }]} /></MemoryRouter>); });
    expect([...host.querySelectorAll('ol li')].map((l) => l.textContent?.replace('›', ''))).toEqual(['Bibliothèque', 'Atelier', 'Support']);
  });
});

describe('onglets portés par l\'URL (CA-N8, CA-A2)', () => {
  function Page() {
    const [tab, setTab] = useTab('/impressions');
    const l = useLocation();
    return <><Tabs label="Vues" value={tab} onChange={setTab} tabs={[{ value: 'a-faire', label: 'À faire' }, { value: 'en-cours', label: 'En cours' }, { value: 'terminees', label: 'Terminées' }, { value: 'bilan', label: 'Bilan' }]} /><output>{tab}|{l.search}</output></>;
  }
  const mount = (url: string) => act(async () => { root.render(<MemoryRouter initialEntries={[url]}><Page /></MemoryRouter>); });
  it('lit ?vue=, retombe sur le premier onglet si la valeur est inconnue, et conserve les autres paramètres', async () => {
    await mount('/impressions?vue=bilan&x=1');
    expect(host.querySelector('output')!.textContent).toBe('bilan|?vue=bilan&x=1');
    await act(async () => { root.unmount(); }); root = createRoot(host);
    await mount('/impressions?vue=n-importe-quoi');
    expect(host.querySelector('output')!.textContent!.startsWith('a-faire|')).toBe(true);
  });
  it('les flèches changent d\'onglet (l\'état suit dans l\'URL)', async () => {
    await mount('/impressions?vue=a-faire');
    const list = host.querySelector('[role="tablist"]')!;
    await act(async () => { list.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true })); });
    expect(host.querySelector('output')!.textContent).toBe('en-cours|?vue=en-cours');
    await act(async () => { list.dispatchEvent(new KeyboardEvent('keydown', { key: 'End', bubbles: true })); });
    expect(host.querySelector('output')!.textContent!.startsWith('bilan|')).toBe(true);
    await act(async () => { list.dispatchEvent(new KeyboardEvent('keydown', { key: 'Home', bubbles: true })); });
    expect(host.querySelector('output')!.textContent!.startsWith('a-faire|')).toBe(true);
    expect(host.querySelectorAll('[role="tab"][tabindex="0"]')).toHaveLength(1);
  });
});

describe('bandeau « Le menu a changé » et barre de chargement', () => {
  it('se ferme et ne revient pas (mémorisé dans le navigateur)', async () => {
    await act(async () => { root.render(<MemoryRouter><MenuChangedBanner /></MemoryRouter>); });
    expect(host.textContent).toContain('Le menu a changé');
    expect(host.querySelector('a[href="/impressions?vue=bilan"]')).not.toBeNull();
    await act(async () => { host.querySelector<HTMLButtonElement>('button[aria-label="Fermer ce message"]')!.click(); });
    expect(host.querySelector('.menu-banner')).toBeNull();
    await act(async () => { root.unmount(); }); root = createRoot(host);
    await act(async () => { root.render(<MemoryRouter><MenuChangedBanner /></MemoryRouter>); });
    expect(host.querySelector('.menu-banner')).toBeNull();
  });
  it('la barre de route est indéterminée (jamais un pourcentage inventé) et absente au repos', async () => {
    await act(async () => { root.render(<NavProgress active />); });
    const bar = host.querySelector('[role="progressbar"]')!;
    expect(bar.getAttribute('aria-label')).toBeTruthy();
    expect(bar.hasAttribute('aria-valuenow')).toBe(false);
    await act(async () => { root.render(<NavProgress active={false} />); });
    expect(host.querySelector('[role="progressbar"]')).toBeNull();
  });
});
