// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { FormCard, ResultView, type AppLike } from './form';
import { buildArgs, formOf, groupFields, initialValues, modelNote, nothingChanged, resultOf, setPath, validate, type FormSpec } from './formLogic';
import { applyHostContext } from '@shared/host';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const PRINTER: FormSpec = {
  form_id: 'add_printer', title: 'Ajouter une imprimante', tool: 'add_printer', submit_tool: 'submit_add_printer', submit_label: "Ajouter l'imprimante", send: 'all',
  intro: "Renseigne ton imprimante Bambu Lab. Le code d'accès LAN n'est PAS demandé ici.",
  fields: [
    { name: 'name', label: 'Nom', kind: 'text', required: true },
    { name: 'model_id', label: 'Modèle', kind: 'select', required: true, options: [{ value: 'p1s', label: 'Bambu Lab P1S' }, { value: 'a1', label: 'Bambu Lab A1' }] },
    { name: 'host', label: 'Adresse IP', kind: 'text', required: true, pattern: '^[A-Za-z0-9.:-]+$' },
    { name: 'serial', label: 'Numéro de série', kind: 'text', required: true, pattern: '^[A-Za-z0-9]{6,32}$', hint: '6 à 32 caractères.' },
    { name: 'nozzle_mm', label: 'Buse', kind: 'segmented', numeric: true, unit: 'mm', options: [{ value: '0.2', label: '0,2' }, { value: '0.4', label: '0,4' }, { value: '0.6', label: '0,6' }] },
    { name: 'ams_units', label: "Nombre d'AMS", kind: 'segmented', numeric: true, options: [{ value: '0', label: 'Aucun' }, { value: '1', label: '1' }] },
  ],
  values: { name: 'Atelier', model_id: '', host: '192.168.1.42', serial: '', nozzle_mm: 0.4, ams_units: 0 },
  missing: ['model_id', 'serial'],
  notice: "Le code d'accès LAN ne se saisit jamais dans cette carte ni dans la conversation.",
};
const sc = (structuredContent: unknown, isError = false, text = 'x') => ({ structuredContent, content: [{ type: 'text', text }], isError });

let host: HTMLElement; let root: Root;
beforeEach(() => { vi.useFakeTimers(); host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host); });
afterEach(() => { act(() => root.unmount()); host.remove(); vi.useRealTimers(); document.documentElement.removeAttribute('data-theme'); });

interface Call { name: string; arguments: Record<string, unknown> }
function mkApp(handlers: Record<string, (a: Record<string, unknown>) => unknown>) {
  const calls: Call[] = [];
  const app = {
    callServerTool: vi.fn(async (p: Call) => { calls.push(p); const h = handlers[p.name]; if (!h) throw new Error('outil inattendu ' + p.name); return h(p.arguments); }),
    openLink: vi.fn(async () => ({})),
    updateModelContext: vi.fn(async () => ({})),
  };
  return { app: app as unknown as AppLike & typeof app, calls };
}
const flush = async () => { await act(async () => { await Promise.resolve(); await Promise.resolve(); }); };
const render = async (el: JSX.Element) => { await act(async () => { root.render(el); }); await flush(); };
const type = (input: HTMLInputElement | HTMLTextAreaElement, v: string) => act(() => {
  const proto = input instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
  Object.getOwnPropertyDescriptor(proto, 'value')!.set!.call(input, v); input.dispatchEvent(new Event('input', { bubbles: true }));
});
const choose = (sel: HTMLSelectElement, v: string) => act(() => { Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!.call(sel, v); sel.dispatchEvent(new Event('change', { bubbles: true })); });
const click = (el: Element) => act(() => { (el as HTMLElement).click(); });
const q = <T extends Element>(sel: string) => host.querySelector(sel) as T | null;
const input = (name: string) => q<HTMLInputElement>(`input[name="${name}"]`)!;
const btn = (re: RegExp) => [...host.querySelectorAll('button, a.btn')].find((b) => re.test(b.textContent ?? '')) as HTMLElement;
// Le bouton agit par onClick (l'envoi natif d'un formulaire est bloqué dans l'iframe sandboxée de l'hôte).
const submit = async () => { await act(async () => { (q<HTMLButtonElement>('.card-foot .btn-primary'))!.click(); }); await flush(); };

describe('logique pure', () => {
  it('reconnaît une carte et un résultat dans le contenu structuré', () => {
    expect(formOf({ form: PRINTER })?.form_id).toBe('add_printer');
    expect(formOf({ form: { form_id: 'x' } })).toBeUndefined();
    expect(formOf(undefined)).toBeUndefined();
    expect(resultOf({ form_result: { ok: true, title: 'Fait', summary: 's' } })?.title).toBe('Fait');
    expect(resultOf({ form_result: {} })).toBeUndefined();
  });
  it('pré-remplit : valeurs du serveur, interrupteurs à faux, cases à []', () => {
    const v = initialValues({ ...PRINTER, fields: [...PRINTER.fields, { name: 'translucent', label: 'T', kind: 'toggle' }, { name: 'ev', label: 'E', kind: 'checks' }] });
    expect(v).toMatchObject({ name: 'Atelier', host: '192.168.1.42', nozzle_mm: 0.4, translucent: false, ev: [] });
  });
  it('valide : obligatoires, motif, bornes, entier, couleur', () => {
    expect(validate(PRINTER, initialValues(PRINTER))).toEqual({ model_id: 'Champ obligatoire.', serial: 'Champ obligatoire.' });
    expect(validate(PRINTER, { ...initialValues(PRINTER), model_id: 'p1s', serial: 'AB' }).serial).toContain('Format invalide');
    const spec: FormSpec = { ...PRINTER, fields: [{ name: 'p', label: 'Prix', kind: 'number', min: 0, max: 10, unit: 'EUR' }, { name: 'n', label: 'N', kind: 'number', integer: true }, { name: 'c', label: 'C', kind: 'color' }] };
    expect(validate(spec, { p: -1, n: 1.5, c: 'rouge' })).toEqual({ p: 'Au moins 0 EUR.', n: 'Nombre entier attendu.', c: 'Couleur au format #RRGGBB.' });
    expect(validate(spec, { p: 11 }).p).toBe('Au plus 10 EUR.');
    expect(validate(spec, { p: 5, n: 2, c: '#aabbcc' })).toEqual({});
  });
  it('construit des objets imbriqués depuis des chemins (tableaux pour les index)', () => {
    const o: Record<string, unknown> = {};
    setPath(o, 'channels.0.name', 'ntfy'); setPath(o, 'channels.0.enabled', true); setPath(o, 'params.length', 40);
    expect(o).toEqual({ channels: [{ name: 'ntfy', enabled: true }], params: { length: 40 } });
  });
  it('send=all : tout sauf les vides, nombres convertis ; send=changed : seulement les modifs + hidden', () => {
    const all = buildArgs(PRINTER, { ...initialValues(PRINTER), model_id: 'p1s', serial: ' 01P00A000000 ', nozzle_mm: 0.6 });
    expect(all).toEqual({ name: 'Atelier', model_id: 'p1s', host: '192.168.1.42', serial: '01P00A000000', nozzle_mm: 0.6, ams_units: 0 });
    const cost: FormSpec = { ...PRINTER, form_id: 'set_cost_settings', send: 'changed', fields: [{ name: 'electricity_per_kwh', label: 'kWh', kind: 'number' }, { name: 'filament_per_kg.PLA', label: 'PLA', kind: 'number' }, { name: 'id', label: 'id', kind: 'hidden' }], values: { electricity_per_kwh: 0.25, 'filament_per_kg.PLA': 20, id: 'a' } };
    expect(buildArgs(cost, { ...initialValues(cost), 'filament_per_kg.PLA': 18.5 })).toEqual({ filament_per_kg: { PLA: 18.5 }, id: 'a' });
    expect(nothingChanged(cost, initialValues(cost))).toBe(true);
  });
  it('regroupe les champs consécutifs et ignore les hidden', () => {
    const g = groupFields([{ name: 'a', label: 'a', kind: 'text', group: 'G1' }, { name: 'h', label: 'h', kind: 'hidden' }, { name: 'b', label: 'b', kind: 'text', group: 'G1' }, { name: 'c', label: 'c', kind: 'text', group: 'G2' }]);
    expect(g.map((x) => [x.title, x.fields.length])).toEqual([['G1', 2], ['G2', 1]]);
  });
  it('la phrase donnée au modèle ne contient jamais les valeurs saisies', () => {
    const t = modelNote(PRINTER, { ok: true, title: 'Imprimante ajoutée', summary: 'Imprimante Atelier enregistrée.' });
    expect(t).toContain('Ajouter une imprimante'); expect(t).not.toMatch(/01P00A|192\.168/);
  });
});

describe('FormCard : ajout d\'imprimante', () => {
  it('affiche le titre, le pré-remplissage, les champs à compléter et le rappel sur le code d\'accès ; aucun champ secret', async () => {
    const { app } = mkApp({});
    await render(<FormCard app={app} spec={PRINTER} />);
    const t = host.textContent ?? '';
    expect(t).toContain('Ajouter une imprimante'); expect(t).toContain('À compléter'); expect(t).toContain('Modèle, Numéro de série');
    expect(t).toContain("Le code d'accès LAN ne se saisit jamais");
    expect(input('name').value).toBe('Atelier'); expect(input('host').value).toBe('192.168.1.42');
    expect(host.querySelector('input[type=password]')).toBeNull();
    expect([...host.querySelectorAll('input, textarea, select')].map((e) => (e.getAttribute('name') ?? '') + (e.getAttribute('autocomplete') ?? '')).join(' ')).not.toMatch(/code|password|secret|token/i);
    expect(host.querySelector('[aria-checked=true]')!.textContent).toBe('0,4'); // buse pré-sélectionnée
  });

  it('refuse d\'envoyer tant qu\'un champ obligatoire manque (aucun appel d\'outil)', async () => {
    const { app, calls } = mkApp({});
    await render(<FormCard app={app} spec={PRINTER} />);
    await submit();
    expect(calls).toHaveLength(0);
    expect(host.textContent).toContain('Champ obligatoire.'); expect(host.querySelectorAll('[role=alert]').length).toBe(2);
    type(input('serial'), 'AB'); await submit();
    expect(host.textContent).toContain('Format invalide'); expect(calls).toHaveLength(0);
  });

  it('valide, appelle submit_add_printer avec les bons types, affiche le résultat, le lien de saisie du code et informe le modèle', async () => {
    const result = { ok: true, title: 'Imprimante ajoutée', summary: 'Imprimante Atelier (Bambu Lab P1S) enregistrée sous prn_1.', details: [{ label: 'Modèle', value: 'Bambu Lab P1S' }], notes: ['Dernière étape : ouvre la page sécurisée.'], links: [{ label: "Saisir le code d'accès LAN (page sécurisée)", url: 'http://127.0.0.1:7878/setup/printer/prn_1' }] };
    const { app, calls } = mkApp({ submit_add_printer: () => sc({ form_result: result }) });
    await render(<FormCard app={app} spec={PRINTER} />);
    choose(q<HTMLSelectElement>('select')!, 'p1s');
    type(input('serial'), '01P00A000000');
    click(btn(/^0,6$/));
    click(btn(/^1$/));
    await submit();
    expect(calls).toEqual([{ name: 'submit_add_printer', arguments: { name: 'Atelier', model_id: 'p1s', host: '192.168.1.42', serial: '01P00A000000', nozzle_mm: 0.6, ams_units: 1 } }]);
    const t = host.textContent ?? '';
    expect(t).toContain('Imprimante ajoutée'); expect(t).toContain('enregistrée sous prn_1'); expect(t).toContain('Bambu Lab P1S'); expect(t).toContain('Dernière étape');
    const link = btn(/Saisir le code d'accès LAN/) as HTMLAnchorElement;
    expect(link.getAttribute('href')).toBe('http://127.0.0.1:7878/setup/printer/prn_1');
    click(link); expect(app.openLink).toHaveBeenCalledWith({ url: 'http://127.0.0.1:7878/setup/printer/prn_1' });
    expect(app.updateModelContext).toHaveBeenCalledTimes(1);
    const sent = JSON.stringify(app.updateModelContext.mock.calls[0]);
    expect(sent).toContain('a validé le formulaire'); expect(sent).not.toContain('01P00A000000');
  });

  it('affiche le refus du serveur (même validation que l\'outil normal) et reste modifiable', async () => {
    const { app } = mkApp({ submit_add_printer: () => sc(undefined, true, "adresse publique refusée : l'imprimante doit être sur le réseau local") });
    await render(<FormCard app={app} spec={{ ...PRINTER, values: { ...PRINTER.values, model_id: 'p1s', serial: '01P00A000000' } }} />);
    await submit();
    expect(q('[role=alert]')!.textContent).toContain('réseau local');
    expect(input('host')).not.toBeNull(); // formulaire toujours là
    expect(app.updateModelContext).not.toHaveBeenCalled();
  });

  it('une exception réseau est affichée, sans planter', async () => {
    const { app } = mkApp({ submit_add_printer: () => { throw new Error('hôte injoignable'); } });
    await render(<FormCard app={app} spec={{ ...PRINTER, values: { ...PRINTER.values, model_id: 'p1s', serial: '01P00A000000' } }} />);
    await submit();
    expect(host.textContent).toContain('hôte injoignable');
  });
});

describe('envoi sans soumission native', () => {
  it('la touche Entrée dans un champ valide aussi, et le formulaire natif est contourné', async () => {
    const { app, calls } = mkApp({ submit_add_printer: () => sc({ form_result: { ok: true, title: 'Fait', summary: 'ok' } }) });
    await render(<FormCard app={app} spec={{ ...PRINTER, values: { ...PRINTER.values, model_id: 'p1s', serial: '01P00A000000' } }} />);
    expect(q('.card-foot .btn-primary')!.getAttribute('type')).toBe('button');
    await act(async () => { input('name').dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })); });
    await flush();
    expect(calls).toHaveLength(1);
  });
});

describe('FormCard : autres formulaires', () => {
  const COST: FormSpec = {
    form_id: 'set_cost_settings', title: 'Réglages de prix', tool: 'set_cost_settings', submit_tool: 'submit_cost_settings', submit_label: 'Enregistrer les prix', send: 'changed',
    fields: [
      { name: 'electricity_per_kwh', label: 'Électricité', kind: 'number', unit: 'EUR/kWh', min: 0, group: 'Électricité' },
      { name: 'filament_per_kg.PLA', label: 'PLA', kind: 'number', unit: 'EUR/kg', min: 0, group: 'Prix du kilo par matière' },
      { name: 'apply_to_history', label: "Recalculer l'historique", kind: 'toggle', group: 'Historique' },
    ],
    values: { electricity_per_kwh: 0.25, 'filament_per_kg.PLA': 20 },
  };
  it('prix : sections, unités, et seules les valeurs modifiées partent', async () => {
    const { app, calls } = mkApp({ submit_cost_settings: () => sc({ form_result: { ok: true, title: 'Prix enregistrés', summary: 'Réglages enregistrés.' } }) });
    await render(<FormCard app={app} spec={COST} />);
    const t = host.textContent ?? '';
    expect(t).toContain('Prix du kilo par matière'); expect(t).toContain('EUR/kWh'); expect(t).toContain('EUR/kg');
    await submit(); expect(host.textContent).toContain('Aucun changement'); expect(calls).toHaveLength(0);
    type(input('electricity_per_kwh'), '0,31');
    await submit();
    expect(calls).toEqual([{ name: 'submit_cost_settings', arguments: { electricity_per_kwh: 0.31 } }]);
    expect(host.textContent).toContain('Prix enregistrés');
  });

  it('notifications : chemins indexés, aucun secret, lien vers la page humaine', async () => {
    const spec: FormSpec = {
      form_id: 'configure_notifications', title: 'Notifications', tool: 'configure_notifications', submit_tool: 'submit_notifications', submit_label: 'Enregistrer', send: 'all',
      fields: [
        { name: 'channels.0.name', label: 'Canal', kind: 'hidden' },
        { name: 'channels.0.enabled', label: 'Canal actif', kind: 'toggle', group: 'ntfy (ntfy)' },
        { name: 'channels.0.events', label: 'Évènements', kind: 'checks', group: 'ntfy (ntfy)', options: [{ value: 'print_completed', label: 'Impression terminée' }, { value: 'print_failed', label: 'Impression échouée' }] },
      ],
      values: { 'channels.0.name': 'ntfy', 'channels.0.enabled': true, 'channels.0.events': ['print_completed', 'print_failed'] },
      links: [{ label: 'Créer ou modifier un canal, saisir les secrets', url: 'http://127.0.0.1:7878/setup/notifications' }],
      notice: 'Les destinations et tous les secrets se saisissent uniquement sur la page sécurisée.',
    };
    const { app, calls } = mkApp({ submit_notifications: () => sc({ form_result: { ok: true, title: 'Notifications enregistrées', summary: '1 canal(aux) réglé(s).' } }) });
    await render(<FormCard app={app} spec={spec} />);
    expect(btn(/saisir les secrets/).getAttribute('href')).toContain('/setup/notifications');
    expect(host.querySelector('input[type=password], input[type=url], input[type=email]')).toBeNull();
    click(host.querySelectorAll('input[type=checkbox]')[2]); // décoche « Impression échouée »
    await submit();
    expect(calls[0].arguments).toEqual({ channels: [{ name: 'ntfy', enabled: true, events: ['print_completed'] }] });
  });

  it('bobine : pastille de couleur, choix usuel, poids et prix', async () => {
    const spec: FormSpec = {
      form_id: 'update_spool', title: 'Ajouter une bobine', tool: 'update_spool', submit_tool: 'submit_spool', submit_label: 'Ajouter la bobine', send: 'all',
      fields: [
        { name: 'material', label: 'Matière', kind: 'select', required: true, options: [{ value: 'PLA', label: 'PLA' }, { value: 'PETG', label: 'PETG' }] },
        { name: 'color_hex', label: 'Couleur', kind: 'color', options: [{ value: '#ffffff', label: 'Blanc', color: '#ffffff' }, { value: '#d62d20', label: 'Rouge', color: '#d62d20' }] },
        { name: 'remaining_g', label: 'Poids restant', kind: 'number', unit: 'g', min: 0 },
        { name: 'price_per_kg', label: 'Prix du kilo', kind: 'number', unit: '€/kg', min: 0 },
      ],
      values: {},
    };
    const { app, calls } = mkApp({ submit_spool: () => sc({ form_result: { ok: true, title: 'Bobine enregistrée', summary: 'Bobine spl_1 : 640 g restant.' } }) });
    await render(<FormCard app={app} spec={spec} />);
    await submit(); expect(calls).toHaveLength(0); // matière obligatoire
    choose(q<HTMLSelectElement>('select')!, 'PETG');
    click(host.querySelector('button[aria-label="Choisir #d62d20"]')!);
    expect((q<HTMLElement>('.swatch-lg'))!.style.background).toContain('rgb(214, 45, 32)');
    type(input('remaining_g'), '640'); type(input('price_per_kg'), '24,5');
    await submit();
    expect(calls[0].arguments).toEqual({ material: 'PETG', color_hex: '#d62d20', remaining_g: 640, price_per_kg: 24.5 });
  });

  it('objectifs : segmentés de priorité, zone de texte obligatoire', async () => {
    const prio = [{ value: '0', label: 'Non' }, { value: '3', label: 'Normal' }, { value: '1', label: 'Prioritaire' }];
    const spec: FormSpec = {
      form_id: 'set_manufacturing_goals', title: 'Objectifs de fabrication', tool: 'set_manufacturing_goals', submit_tool: 'submit_goals', submit_label: "Enregistrer l'objectif", send: 'all',
      fields: [
        { name: 'original_text', label: 'Le besoin', kind: 'textarea', required: true },
        { name: 'strength', label: 'Solide', kind: 'segmented', numeric: true, options: prio, group: 'Ce qui compte' },
        { name: 'speed', label: 'Rapide', kind: 'segmented', numeric: true, options: prio, group: 'Ce qui compte' },
      ],
      values: { strength: 0, speed: 0 },
    };
    const { app, calls } = mkApp({ submit_goals: () => sc({ form_result: { ok: true, title: 'Objectif enregistré', summary: 'Objectif g_1 : 2 critère(s).' } }) });
    await render(<FormCard app={app} spec={spec} />);
    await submit(); expect(calls).toHaveLength(0);
    type(q<HTMLTextAreaElement>('textarea')!, 'un support solide');
    const prioritaire = [...host.querySelectorAll('button')].filter((b) => b.textContent === 'Prioritaire');
    click(prioritaire[0]); click([...host.querySelectorAll('button')].filter((b) => b.textContent === 'Normal')[1]);
    await submit();
    expect(calls[0].arguments).toEqual({ original_text: 'un support solide', strength: 1, speed: 3 });
  });

  it('gabarit : paramètres générés (unité, bornes), aperçu mis à jour, résultat avec image', async () => {
    const spec: FormSpec = {
      form_id: 'create_from_template', title: 'Collier de serrage', tool: 'create_from_template', submit_tool: 'submit_part_template', submit_label: 'Créer la pièce', send: 'all', preview_tool: 'preview_part_template',
      fields: [
        { name: 'template', label: 'Gabarit', kind: 'hidden' },
        { name: 'name', label: 'Nom du modèle', kind: 'text' },
        { name: 'params.diameter', label: 'Diameter', kind: 'number', unit: 'mm', min: 5, max: 100, group: 'Paramètres de la pièce' },
        { name: 'params.style', label: 'Style', kind: 'select', options: [{ value: 'a', label: 'a' }, { value: 'b', label: 'b' }], group: 'Paramètres de la pièce' },
      ],
      values: { template: 'clamp', 'params.diameter': 32, 'params.style': 'a' },
    };
    const png = 'AAAA';
    const { app, calls } = mkApp({
      preview_part_template: () => sc({ image: { mime: 'image/svg+xml', data: png } }),
      submit_part_template: () => sc({ form_result: { ok: true, title: 'Pièce créée', summary: 'Modèle mdl_1 créé.', image: { mime: 'image/svg+xml', data: 'BBBB', alt: 'Aperçu de la pièce créée' } } }),
    });
    await render(<FormCard app={app} spec={spec} previewDebounceMs={50} />);
    await act(async () => { vi.advanceTimersByTime(60); }); await flush();
    expect(calls[0]).toEqual({ name: 'preview_part_template', arguments: { template: 'clamp', params: { diameter: 32, style: 'a' } } });
    expect(q<HTMLImageElement>('.form-preview img')!.src).toContain('data:image/svg+xml;base64,AAAA');
    type(input('params.diameter'), '40');
    await act(async () => { vi.advanceTimersByTime(60); }); await flush();
    expect(calls.at(-1)!.arguments).toEqual({ template: 'clamp', params: { diameter: 40, style: 'a' } }); // aperçu mis à jour
    type(input('params.diameter'), '3'); await submit(); // < min
    expect(host.textContent).toContain('Au moins 5 mm.');
    type(input('params.diameter'), '40'); await submit();
    expect(calls.at(-1)).toEqual({ name: 'submit_part_template', arguments: { template: 'clamp', params: { diameter: 40, style: 'a' } } });
    expect(host.textContent).toContain('Pièce créée');
    expect(q<HTMLImageElement>('figure img')!.src).toContain('BBBB');
  });
});

describe('ResultView et thème', () => {
  it('résumé d\'un outil appelé avec tous ses champs : détails, notes, lien', async () => {
    const { app } = mkApp({});
    await render(<ResultView app={app} result={{ ok: true, title: 'Objectif enregistré', summary: 'Objectif g_1 : 2 critère(s).', details: [{ label: 'Solide', value: 'priorité 1' }], notes: ['Question : direction de l\'effort ?'], links: [{ label: 'Ouvrir', url: 'http://x/y' }] }} />);
    const t = host.textContent ?? '';
    expect(t).toContain('Objectif enregistré'); expect(t).toContain('priorité 1'); expect(t).toContain('direction de l\'effort'); expect(btn(/Ouvrir/)).not.toBeUndefined();
  });
  it('un échec est annoncé (carte en erreur, rôle status)', async () => {
    const { app } = mkApp({});
    await render(<ResultView app={app} result={{ ok: false, title: 'Refusé', summary: 'Prix invalide.' }} />);
    expect(q('.card-err')).not.toBeNull(); expect(host.textContent).toContain('Échec');
  });
  it('mode sombre : le thème de l\'hôte pose data-theme et la carte reste rendue avec les mêmes classes', async () => {
    applyHostContext({ theme: 'dark' });
    const { app } = mkApp({});
    await render(<FormCard app={app} spec={PRINTER} />);
    expect(document.documentElement.dataset.theme).toBe('dark');
    expect(q('.card.form-card')).not.toBeNull(); expect(q('.btn-primary')!.textContent).toContain("Ajouter l'imprimante");
    applyHostContext({ theme: 'light' }); expect(document.documentElement.dataset.theme).toBe('light');
  });
  it('un nouveau résultat d\'outil remplace le formulaire et efface le précédent résultat', async () => {
    const { app } = mkApp({ submit_add_printer: () => sc({ form_result: { ok: true, title: 'Fait', summary: 'ok' } }) });
    await render(<FormCard app={app} spec={{ ...PRINTER, values: { ...PRINTER.values, model_id: 'p1s', serial: '01P00A000000' } }} />);
    await submit(); expect(host.textContent).toContain('Fait');
    await render(<FormCard app={app} spec={{ ...PRINTER, title: 'Autre carte' }} />);
    expect(host.textContent).toContain('Autre carte'); expect(host.textContent).not.toContain('ok');
  });
});
