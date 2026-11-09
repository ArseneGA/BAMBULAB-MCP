// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest';
import { CostsCard, LiteBars, normalizeCosts } from './costs';
import { DASHBOARD, ESTIMATE, PROJECTS } from './fixtures';
import { mockApp, mount } from './testkit';

let m: ReturnType<typeof mount>;
afterEach(() => m?.unmount());
const view = (s: unknown) => <CostsCard app={mockApp()} data={normalizeCosts(s)!} onOpen={() => {}} />;

describe('carte Coûts', () => {
  it('distingue les trois formes', () => {
    expect(normalizeCosts(ESTIMATE)?.kind).toBe('estimate');
    expect(normalizeCosts(PROJECTS)?.kind).toBe('projects');
    expect(normalizeCosts(DASHBOARD)?.kind).toBe('dashboard');
    expect(normalizeCosts({ spools: [] })).toBeUndefined();
  });

  it('estimate_cost : tuiles, ventilation matière / électricité / usure, détail, hypothèses', async () => {
    m = mount();
    await m.render(view(ESTIMATE));
    const t = m.text();
    expect(t).toContain('0,92 €'); expect(t).toContain('réel estimé 0,98 €'); expect(t).toContain('34,2 g');
    expect(t).toContain('Matière'); expect(t).toContain('Électricité'); expect(t).toContain('0,23 kWh'); expect(t).toContain('Usure');
    expect(t).toContain('PETG'); expect(t).toContain('Hypothèses (2)'); expect(t).toContain('Estimation');
    expect(m.host.querySelectorAll('[role=meter]')).toHaveLength(3);
  });

  it('get_project_costs : total, projets, jauges, préparées non terminées', async () => {
    m = mount();
    await m.render(view(PROJECTS));
    const t = m.text();
    expect(t).toContain('10,70 €'); expect(t).toContain('Cuisine'); expect(t).toContain('Atelier'); expect(t).toContain('7,80 €');
    expect(t).toContain('1 préparée(s)'); expect(m.host.querySelectorAll('li.lc-row')).toHaveLength(2);
  });

  it('get_usage_dashboard : tuiles, barres SVG par mois, bascule coût / heures, détail matière', async () => {
    m = mount();
    await m.render(view(DASHBOARD));
    const t = m.text();
    expect(t).toContain('14'); expect(t).toContain('86 %'); expect(t).toContain('48,5 h'); expect(t).toContain('820 g'); expect(t).toContain('19,40 €');
    const bars = () => [...m.host.querySelectorAll('svg.lc-bars rect')];
    expect(bars()).toHaveLength(3);
    expect(m.host.querySelector('svg.lc-bars')?.textContent).toContain('sept'); expect(m.host.querySelector('svg.lc-bars')?.textContent).toContain('nov');
    expect(bars()[1].querySelector('title')?.textContent).toContain('9,30 €');
    const h = (b: Element) => Number(b.getAttribute('height'));
    expect(h(bars()[1])).toBeGreaterThan(h(bars()[0])); // octobre > septembre
    await m.click(/^Heures$/);
    expect(bars()[1].querySelector('title')?.textContent).toContain('22,5 h');
    expect(t).toContain('PLA'); expect(t).toContain('PETG');
  });

  it('états vides : aucune impression, aucun projet ; barres à zéro sans hauteur', async () => {
    m = mount();
    await m.render(view({ ...DASHBOARD, totals: { ...DASHBOARD.totals, prints: 0 } }));
    expect(m.text()).toContain('Aucune impression sur cette période');
    m.unmount(); m = mount();
    await m.render(view({ ...PROJECTS, projects: [] }));
    expect(m.text()).toContain('Aucune impression terminée');
    m.unmount(); m = mount();
    await m.render(<LiteBars label="zéro" bars={[{ label: 'a', value: 0, title: 'a' }]} />);
    expect(m.host.querySelector('rect')?.getAttribute('height')).toBe('0');
  });
});
