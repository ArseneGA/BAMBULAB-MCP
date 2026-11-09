// @vitest-environment jsdom
import { act, useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { Badge, Button, Card, CheckGroup, ColorField, EmptyState, Notice, NumberField, ProgressBar, Segmented, SelectField, Table, TextField, ToastProvider, useToast } from './index';
import { applyHostContext, applyTheme, luminanceOf, themeFromHost } from '../host';
import { Printer } from '../icons';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLElement; let root: Root;
beforeEach(() => { host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host); });
afterEach(() => { act(() => root.unmount()); host.remove(); document.documentElement.removeAttribute('data-theme'); document.documentElement.removeAttribute('style'); });
const render = (el: JSX.Element) => act(() => { root.render(el); });
const html = (el: JSX.Element) => renderToStaticMarkup(el);
const q = <T extends Element>(sel: string) => host.querySelector(sel) as T | null;
const type = (input: HTMLInputElement, v: string) => act(() => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, v); input.dispatchEvent(new Event('input', { bubbles: true })); });

describe('Card', () => {
  it('affiche titre, icône, méta, corps et pied ; la teinte ajoute une classe', () => {
    const out = html(<Card title="Imprimantes" icon={<Printer size={16} />} meta={<span>2 en ligne</span>} footer={<Button>OK</Button>} tone="ok">corps</Card>);
    expect(out).toContain('class="card card-ok"');
    expect(out).toContain('<h2 class="card-title">Imprimantes</h2>');
    expect(out).toContain('2 en ligne'); expect(out).toContain('card-foot'); expect(out).toContain('corps');
    expect(out).toContain('<svg'); // icône au trait
  });
  it('sans titre ni pied : corps seul', () => {
    const out = html(<Card>seul</Card>);
    expect(out).not.toContain('card-head'); expect(out).not.toContain('card-foot');
  });
});

describe('Badge', () => {
  it('porte la teinte et un point (la couleur n\'est jamais seule)', () => {
    expect(html(<Badge tone="err">Échec</Badge>)).toContain('badge badge-err');
    expect(html(<Badge tone="run">En cours</Badge>)).toContain('<i aria-hidden="true">');
    expect(html(<Badge dot={false}>x</Badge>)).not.toContain('<i');
  });
});

describe('Button', () => {
  it('a la hauteur de contrôle de 34 px (classe btn, jeton --control-h)', () => {
    expect(html(<Button variant="primary">Valider</Button>)).toContain('class="btn btn-primary"');
    expect(html(<Button size="sm">Petit</Button>)).toContain('btn-sm');
  });
});

describe('NumberField', () => {
  it('affiche l\'unité, accepte la virgule décimale et vide = undefined', () => {
    const seen: Array<number | undefined> = [];
    const Harness = () => { const [v, setV] = useState<number | undefined>(0.25); return <NumberField label="Électricité" unit="EUR/kWh" value={v} onChange={(n) => { seen.push(n); setV(n); }} />; };
    render(<Harness />);
    expect(host.textContent).toContain('EUR/kWh');
    const input = q<HTMLInputElement>('input')!;
    expect(input.value).toBe('0.25');
    type(input, '0,31'); expect(seen.at(-1)).toBe(0.31);
    type(input, ''); expect(seen.at(-1)).toBeUndefined();
  });
  it('relie le message d\'erreur au champ (aria-invalid, role=alert)', () => {
    render(<NumberField label="Prix" value={-1} onChange={() => {}} error="Au moins 0." />);
    expect(q('input')!.getAttribute('aria-invalid')).toBe('true');
    expect(q('[role=alert]')!.textContent).toBe('Au moins 0.');
  });
});

describe('champs texte, liste, couleur, cases', () => {
  it('TextField et SelectField : libellé associé, obligatoire signalé', () => {
    render(<><TextField label="Nom" required value="" onChange={() => {}} /><SelectField label="Modèle" value="" onChange={() => {}} options={[{ value: 'p1s', label: 'P1S' }]} placeholder="Choisir…" required /></>);
    expect(host.querySelectorAll('label').length).toBe(2);
    expect(host.textContent).toContain('Choisir…'); expect(host.textContent).toContain('*');
  });
  it('ColorField : pastille, #RRGGBB, couleurs usuelles', () => {
    const seen: string[] = [];
    const Harness = () => { const [v, setV] = useState('#16a06b'); return <ColorField label="Couleur" value={v} presets={['#ffffff', '#111111']} onChange={(c) => { seen.push(c); setV(c); }} />; };
    render(<Harness />);
    expect(q<HTMLElement>('.swatch-lg')!.style.background).toBeTruthy();
    act(() => { (host.querySelector('button[aria-label="Choisir #111111"]') as HTMLElement).click(); });
    expect(seen.at(-1)).toBe('#111111');
    type(q<HTMLInputElement>('input')!, 'ff0000'); expect(seen.at(-1)).toBe('#ff0000'); // # ajouté
  });
  it('CheckGroup coche et décoche', () => {
    const seen: string[][] = [];
    const Harness = () => { const [v, setV] = useState<string[]>(['a']); return <CheckGroup label="Évènements" options={[{ value: 'a', label: 'A' }, { value: 'b', label: 'B' }]} values={v} onChange={(n) => { seen.push(n); setV(n); }} />; };
    render(<Harness />);
    act(() => { (host.querySelectorAll('input')[1] as HTMLElement).click(); }); expect(seen.at(-1)).toEqual(['a', 'b']);
    act(() => { (host.querySelectorAll('input')[0] as HTMLElement).click(); }); expect(seen.at(-1)).toEqual(['b']);
  });
  it('Segmented : un seul choix, role=radiogroup', () => {
    const seen: string[] = [];
    render(<Segmented label="Buse" value="0.4" onChange={(v) => seen.push(v)} options={[{ value: '0.2', label: '0,2' }, { value: '0.4', label: '0,4' }]} />);
    expect(q('[role=radiogroup]')).not.toBeNull();
    expect(host.querySelector('[aria-checked=true]')!.textContent).toBe('0,4');
    act(() => { (host.querySelectorAll('button')[0] as HTMLElement).click(); }); expect(seen).toEqual(['0.2']);
  });
});

describe('Table, Notice, EmptyState, ProgressBar, Toast', () => {
  it('Table : en-têtes, lignes, alignement à droite, vide', () => {
    const rows = [{ id: 'a', n: 'PLA', p: '20' }];
    const out = html(<Table columns={[{ key: 'n', header: 'Matière' }, { key: 'p', header: 'Prix', align: 'right', mono: true }]} rows={rows} getKey={(r) => r.id} caption="Prix" />);
    expect(out).toContain('>Matière</th>'); expect(out).toContain('class="r mono"'); expect(out).toContain('>PLA</td>');
    expect(html(<Table columns={[]} rows={[]} getKey={() => ''} empty={<EmptyState title="Rien" />} />)).toContain('Rien');
  });
  it('Notice : rôle alert pour une erreur', () => {
    expect(html(<Notice tone="err" title="Refusé">raison</Notice>)).toContain('role="alert"');
    expect(html(<Notice tone="ok">fait</Notice>)).toContain('notice-ok');
  });
  it('ProgressBar : pourcentage, role=progressbar, indéterminée sans valeur', () => {
    const out = html(<ProgressBar label="Découpe" source={{ state: 'running', percent: 42 }} />);
    expect(out).toContain('aria-valuenow="42"'); expect(out).toContain('width:42%');
    expect(html(<ProgressBar label="x" source={{ state: 'running' }} />)).toContain('is-indet');
  });
  it('Toast : affiche puis se ferme', () => {
    const Say = () => { const t = useToast(); return <button onClick={() => t('ok', 'Enregistré')}>go</button>; };
    render(<ToastProvider><Say /></ToastProvider>);
    act(() => { (q('button') as HTMLElement).click(); });
    expect(host.textContent).toContain('Enregistré');
    act(() => { (host.querySelector('button[aria-label="Fermer"]') as HTMLElement).click(); });
    expect(host.textContent).not.toContain('Enregistré');
  });
});

describe('thème de l\'hôte (clair / sombre)', () => {
  it('lit theme, sinon la luminance du fond annoncé par l\'hôte', () => {
    expect(themeFromHost({ theme: 'dark' })).toBe('dark');
    expect(themeFromHost({ styles: { variables: { '--color-background-primary': '#1a1a1a' } } })).toBe('dark');
    expect(themeFromHost({ styles: { variables: { '--color-background-primary': 'rgb(250, 249, 245)' } } })).toBe('light');
    expect(themeFromHost({})).toBeUndefined();
    expect(luminanceOf('nope')).toBeUndefined();
  });
  it('applique data-theme sur <html> et recopie les variables sous --host-, sans écraser la police de la charte', () => {
    const t = applyHostContext({ theme: 'dark', styles: { variables: { '--font-sans': 'Anthropic Sans', '--color-text-primary': '#fff' } } });
    expect(t).toBe('dark');
    expect(document.documentElement.dataset.theme).toBe('dark');
    expect(document.documentElement.style.getPropertyValue('--host-font-sans')).toBe('Anthropic Sans');
    expect(document.documentElement.style.getPropertyValue('--font-sans')).toBe('');
  });
  it('une notification partielle ne réinitialise pas le thème', () => {
    applyTheme('dark');
    expect(applyHostContext({ styles: { variables: {} } })).toBeUndefined();
    expect(document.documentElement.dataset.theme).toBe('dark');
  });
});
