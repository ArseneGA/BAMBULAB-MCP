// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest';
import { PrintersCard, normalizePrinters } from './printers';
import { PRINTERS, PRINTER_STATUS } from './fixtures';
import { mockApp, mount, ok } from './testkit';

let m: ReturnType<typeof mount>;
afterEach(() => m?.unmount());
const data = () => normalizePrinters(PRINTERS)!;

describe('carte Imprimantes', () => {
  it('reconnaît list_printers seulement', () => {
    expect(normalizePrinters(PRINTERS)?.printers).toHaveLength(2);
    expect(normalizePrinters({ printers: [{ nope: 1 }] })).toBeUndefined();
    expect(normalizePrinters({ models: [] })).toBeUndefined();
  });

  it('machines : état relu, firmware, AMS, dernière impression ; aucun secret', async () => {
    const app = mockApp({ get_printer_status: () => ok(PRINTER_STATUS) });
    m = mount();
    await m.render(<PrintersCard app={app} data={data()} onOpen={() => {}} />);
    const t = m.text();
    expect(t).toContain('P1S atelier'); expect(t).toContain('Bambu Lab P1S'); expect(t).toContain('1 AMS'); expect(t).toContain('firmware 01.08.02.00');
    expect(t).toContain('En impression · 42 %'); expect(t).toContain('Dernière impression : Boîte à épices (terminée'); expect(t).toContain('support-cables.3mf');
    expect(t).toContain('A1 mini bureau'); expect(t).toContain('sans AMS'); expect(t).toContain('Code d\'accès à saisir');
    // une seule lecture : l'imprimante sans code d'accès n'est pas interrogée
    expect(app.callServerTool.mock.calls.map((c) => c[0].arguments.printer_id)).toEqual(['pr_1']);
    for (const secret of ['01P00A000000000', '192.168.1.50', 'setup/pr_2', 'access_code']) expect(m.host.innerHTML).not.toContain(secret);
  });

  it('clic -> get_printer_status, résultat transmis à la vue', async () => {
    const res = ok(PRINTER_STATUS);
    const app = mockApp({ get_printer_status: () => res });
    const opened: unknown[] = [];
    m = mount();
    await m.render(<PrintersCard app={app} data={data()} onOpen={(r) => opened.push(r)} />);
    app.callServerTool.mockClear();
    await m.click(/État détaillé de P1S atelier/);
    expect(app.callServerTool).toHaveBeenCalledWith({ name: 'get_printer_status', arguments: { printer_id: 'pr_1' } });
    expect(opened).toEqual([res]);
  });

  it('injoignable, état illisible, liste vide, erreur de clic', async () => {
    const app = mockApp({ get_printer_status: () => ok({ ...PRINTER_STATUS, reachable: false, state: 'offline' }) });
    m = mount();
    await m.render(<PrintersCard app={app} data={data()} onOpen={() => {}} />);
    expect(m.text()).toContain('Injoignable');
    m.unmount(); m = mount();
    const bad = mockApp({ get_printer_status: () => { throw new Error('panne'); } });
    await m.render(<PrintersCard app={bad} data={data()} onOpen={() => {}} />);
    expect(m.text()).toContain('État inconnu');
    await m.click(/État détaillé de P1S atelier/);
    expect(m.host.querySelector('[role=alert]')?.textContent).toContain('panne');
    m.unmount(); m = mount();
    await m.render(<PrintersCard app={mockApp()} data={{ printers: [] }} onOpen={() => {}} />);
    expect(m.text()).toContain('Aucune imprimante enregistrée');
  });
});
