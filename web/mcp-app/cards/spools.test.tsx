// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest';
import { SpoolsCard, normalizeSpools } from './spools';
import { AMS, SPOOLS } from './fixtures';
import { mockApp, mount } from './testkit';

let m: ReturnType<typeof mount>;
afterEach(() => m?.unmount());
const view = (s: unknown) => <SpoolsCard app={mockApp()} data={normalizeSpools(s)!} onOpen={() => {}} />;

describe('carte Bobines', () => {
  it('reconnaît list_spools et get_ams_state', () => {
    expect(normalizeSpools(SPOOLS)?.kind).toBe('spools');
    expect(normalizeSpools(AMS)?.kind).toBe('ams');
    expect(normalizeSpools({ queue: [] })).toBeUndefined();
  });

  it('inventaire : pastille de couleur réelle, matière, reste en g + jauge, alerte manque', async () => {
    m = mount();
    await m.render(view(SPOOLS));
    const t = m.text();
    expect(t).toContain('Bambu Lab Vert jade'); expect(t).toContain('720 g'); expect(t).toContain('72 %'); expect(t).toContain('PETG'); expect(t).toContain('translucide');
    expect(t).toContain('Manque'); expect(t).toContain('Vide'); expect(t).toContain('2 presque vide(s)');
    const sw = [...m.host.querySelectorAll<HTMLElement>('.lc-swatch')];
    expect(sw[0].style.background).toMatch(/rgb\(0, 174, 66\)|#00ae42/); // vert jade réel
    expect(sw[2].className).toContain('lc-swatch-none'); // couleur inconnue : jamais inventée
    const meters = [...m.host.querySelectorAll('[role=meter]')];
    expect(meters.map((x) => x.getAttribute('aria-valuenow'))).toEqual(['72', '6', '0']);
    expect(m.text()).toContain('Alerte de manque : marge de 10 %');
  });

  it('AMS : emplacements, actif, vide, divergence, humidité, couleur RRGGBBAA', async () => {
    m = mount();
    await m.render(view(AMS));
    const t = m.text();
    expect(t).toContain('ams1:1'); expect(t).toContain('Actif'); expect(t).toContain('Emplacement vide'); expect(t).toContain('Non inscrite'); expect(t).toContain('Reste faible.'); expect(t).toContain('AMS 1 : 2/5');
    expect(t).toContain('PLA · PLA Basic');
    const sw = [...m.host.querySelectorAll<HTMLElement>('.lc-swatch')];
    expect(sw).toHaveLength(4);
    expect(sw[0].style.background).toMatch(/rgb\(0, 174, 66\)|#00ae42/);
    expect(sw[2].className).toContain('lc-swatch-none');
    expect(m.host.querySelectorAll('li.lc-row')).toHaveLength(4);
  });

  it('états : inventaire vide, imprimante injoignable, aucun AMS', async () => {
    m = mount();
    await m.render(view({ ...SPOOLS, spools: [] }));
    expect(m.text()).toContain('Aucune bobine');
    m.unmount(); m = mount();
    await m.render(view({ ...AMS, reachable: false, error: 'imprimante injoignable', trays: [] }));
    expect(m.text()).toContain('Injoignable'); expect(m.host.querySelector('[role=alert]')?.textContent).toContain('imprimante injoignable');
    m.unmount(); m = mount();
    await m.render(view({ ...AMS, ams_present: false, trays: [] }));
    expect(m.text()).toContain('Aucun AMS');
  });
});
