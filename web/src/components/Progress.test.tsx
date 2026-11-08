import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { ProgressBar } from './Progress';
import { printSource } from '../api/progress';
import { LOGO_URL, PRODUCT_NAME, TAGLINE, pageTitle } from '../brand';
import { mainNav, moreNav, workshopNav } from '../Shell';

const html = (el: JSX.Element) => renderToStaticMarkup(el);

describe('ProgressBar', () => {
  it('affiche le pourcentage, la phase, le temps restant et expose role=progressbar', () => {
    const out = html(<ProgressBar label="Avancement de la découpe" source={{ state: 'running', percent: 61, phase_label: 'Découpe par Bambu Studio', eta_seconds: 9 * 3600 + 38 * 60 }} />);
    expect(out).toContain('role="progressbar"');
    expect(out).toContain('aria-valuenow="61"');
    expect(out).toContain('61 %');
    expect(out).toContain('Découpe par Bambu Studio');
    expect(out).toContain('reste 9 h 38');
    expect(out).toContain('width:61%');
  });
  it('reste indéterminée sans pourcentage (pas de faux 0 %)', () => {
    const out = html(<ProgressBar label="Import" source={{ state: 'running' }} />);
    expect(out).toContain('is-indet');
    expect(out).not.toContain('aria-valuenow');
  });
  it('retombe sur Job.progress quand les champs stables sont absents', () => {
    const out = html(<ProgressBar label="Import" source={{ state: 'running', progress: 0.5, message: 'Copie 3/6' }} />);
    expect(out).toContain('aria-valuenow="50"');
    expect(out).toContain('Copie 3/6');
  });
  it('signale un travail en file et un travail bloqué', () => {
    expect(html(<ProgressBar label="x" source={{ state: 'queued', percent: 0, queue_position: 2 }} />)).toContain('position 2');
    const stalled = html(<ProgressBar label="x" source={{ state: 'running', percent: 30, stalled: true }} />);
    expect(stalled).toContain('is-warn');
    expect(stalled).toContain('bloqué');
  });
  it('affiche les octets d\'un transfert', () => {
    expect(html(<ProgressBar label="Envoi" source={{ state: 'running', done_bytes: 5 << 20, total_bytes: 10 << 20 }} />)).toContain('5,0 Mio / 10,0 Mio');
  });
});

describe('printSource', () => {
  it('prend l\'état réel de l\'imprimante pour une impression en cours', () => {
    const s = printSource({ state: 'PRINTING' }, { percent: 10 }, { reachable: true, progress_percent: 61, remaining_minutes: 578, layer: 514, total_layers: 841 });
    expect(s.percent).toBe(61);
    expect(s.remaining_minutes).toBe(578);
    expect(s.phase_label).toBe('Impression · couche 514 sur 841');
  });
  it('retombe sur la dernière observation du service si l\'imprimante ne répond pas', () => {
    const s = printSource({ state: 'PRINTING', print: { percent: 40, layer: 10, total_layers: 20, remaining_minutes: 30 } }, undefined, { reachable: false });
    expect(s.percent).toBe(40);
  });
  it('transmet le JobProgress d\'une découpe', () => {
    const s = printSource({ state: 'SLICING' }, { percent: 33, phase: 'découpe par Bambu Studio', eta_seconds: 80 });
    expect(s.percent).toBe(33);
    expect(s.eta_seconds).toBe(80);
  });
});

describe('identité du produit', () => {
  it('le nom et la baseline viennent d\'une constante unique', () => {
    expect(PRODUCT_NAME).toBe('Printalk');
    expect(TAGLINE).toBe('Fabriquer en parlant');
    expect(pageTitle()).toBe('Printalk');
    expect(pageTitle('Bibliothèque')).toBe('Bibliothèque · Printalk');
    expect(LOGO_URL).toMatch(/atelier-icon\.svg$/);
  });
});

describe('navigation', () => {
  const all = [...mainNav, ...workshopNav, ...moreNav].map((n) => n.path);
  it('contient les écrans de la maquette et les écrans annexes, sans doublon', () => {
    for (const p of ['', 'bibliotheque', 'preparation', 'impressions', 'imprimantes', 'bobines', 'couts', 'gabarits', 'materiaux', 'corbeille', 'reglages']) expect(all).toContain(p);
    expect(new Set(all).size).toBe(all.length);
  });
});
