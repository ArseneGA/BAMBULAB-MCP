// @vitest-environment jsdom
import { act, useState } from 'react';
import { createRoot } from 'react-dom/client';
import type { Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { EmptyState, ErrorState, Skeleton, Tabs } from './index';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLElement; let root: Root;
beforeEach(() => { host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host); });
afterEach(() => { act(() => root.unmount()); host.remove(); });
const render = (el: JSX.Element) => act(() => { root.render(el); });

describe('états (G10 UX5, CA-A1)', () => {
  it('un état vide a trois éléments : ce que c\'est, pourquoi, une action', async () => {
    await render(<EmptyState title="Aucun projet" text="Un projet regroupe les pièces d'un même objet." action={<button type="button">Créer un projet</button>} />);
    expect(host.querySelector('h3')?.textContent).toBe('Aucun projet');
    expect(host.querySelector('p')?.textContent).toContain('regroupe');
    expect(host.querySelectorAll('button')).toHaveLength(1);
  });
  it('une erreur a trois parties et replie le détail technique', async () => {
    await render(<ErrorState title="Cette partie n'a pas pu se charger" text="La requête a échoué." impact="Le reste de la page reste utilisable." action={<button type="button">Réessayer</button>} details="HTTP 500 sur /api/x" />);
    const alert = host.querySelector('[role="alert"]')!;
    expect(alert.querySelector('h3')?.textContent).toContain('pas pu se charger');
    expect(alert.querySelector('.state-impact')?.textContent).toContain('reste utilisable');
    expect(alert.querySelector('button')?.textContent).toBe('Réessayer');
    const details = alert.querySelector('details')!;
    expect(details.querySelector('summary')?.textContent).toBe('Détails techniques');
    expect(details.hasAttribute('open')).toBe(false);
  });
  it('un squelette annonce le chargement (aria-busy, role=status) sans spinner global', async () => {
    await render(<Skeleton lines={3} label="Chargement des impressions" />);
    const sk = host.querySelector('[role="status"]')!;
    expect(sk.getAttribute('aria-busy')).toBe('true');
    expect(sk.getAttribute('aria-label')).toBe('Chargement des impressions');
    expect(sk.querySelectorAll('.skeleton')).toHaveLength(3);
    expect(host.querySelector('.spinner')).toBeNull();
  });
});

describe('onglets au clavier (CA-A2)', () => {
  function Demo() {
    const [v, setV] = useState('a');
    return <><Tabs label="Vues" value={v} onChange={setV} tabs={[{ value: 'a', label: 'A' }, { value: 'b', label: 'B' }, { value: 'c', label: 'C', disabled: true }, { value: 'd', label: 'D' }]} /><output>{v}</output></>;
  }
  const key = (k: string) => act(async () => { host.querySelector('[role="tablist"]')!.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true })); });
  it('flèches, Début et Fin changent d\'onglet en sautant les onglets désactivés ; un seul onglet est dans l\'ordre de tabulation', async () => {
    await render(<Demo />);
    await key('ArrowRight'); expect(host.querySelector('output')!.textContent).toBe('b');
    await key('ArrowRight'); expect(host.querySelector('output')!.textContent).toBe('d');
    await key('ArrowRight'); expect(host.querySelector('output')!.textContent).toBe('a');
    await key('ArrowLeft'); expect(host.querySelector('output')!.textContent).toBe('d');
    await key('Home'); expect(host.querySelector('output')!.textContent).toBe('a');
    await key('End'); expect(host.querySelector('output')!.textContent).toBe('d');
    expect(host.querySelectorAll('[role="tab"][tabindex="0"]')).toHaveLength(1);
    expect(host.querySelector('[role="tab"][aria-selected="true"]')?.textContent).toBe('D');
  });
});
