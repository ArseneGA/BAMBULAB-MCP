// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { CODE_RE, LaunchConfirm, StopConfirm, stopIdOf, type AppLike } from './launch';
import type { ConfirmationView } from '../src/api/printTypes';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const CONF: ConfirmationView = {
  confirmation_id: 'cf_1', print_job_id: 'pj_1', expires_at: new Date(Date.now() + 600_000).toISOString(), confirm_url: 'http://127.0.0.1:7878/confirm/cf_1', approved: false, state: 'AWAITING_PRINT_CONFIRMATION',
  instructions: "Demande à l'utilisateur d'ouvrir confirm_url.",
  binding: { file: 'boite.gcode.3mf', printer_name: 'Atelier', printer_model: 'P1S', sha256: 'a'.repeat(64), plate: 1, materials: [{ filament_slot: 1, tray: 'ams0:1', material_id: 'PLA' }], estimated_minutes: 95, estimated_grams: 12.3 },
  safety: { analysed: true, nozzle_start_max_c: 250, nozzle_first_layer_c: 220, nozzle_print_min_c: 220, nozzle_print_max_c: 220, bed_first_layer_c: 55, bed_min_c: 55, bed_max_c: 55, gcode_profile_line: 'G-code de démarrage : profil officiel ✓', gcode_official: true, anomalies: [] },
};
const sc = (structuredContent: unknown, isError = false) => ({ structuredContent, content: [{ type: 'text', text: 'x' }], isError });
const STATE = { confirmation_id: 'cf_1', approved: false, code_set: true, locked: false, attempts_left: 3, setup_url: 'http://127.0.0.1:7878/setup/launch-code' };

let host: HTMLElement; let root: Root;
beforeEach(() => { host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host); });
afterEach(() => { act(() => root.unmount()); host.remove(); });

interface Call { name: string; arguments: Record<string, unknown> }
function mkApp(handlers: Record<string, (a: Record<string, unknown>) => unknown>) {
  const calls: Call[] = [];
  const app = {
    callServerTool: vi.fn(async (p: Call) => { calls.push(p); const h = handlers[p.name]; if (!h) throw new Error('outil inattendu ' + p.name); return h(p.arguments); }),
    openLink: vi.fn(async () => ({})),
    updateModelContext: vi.fn(), sendMessage: vi.fn(),
  };
  return { app: app as unknown as AppLike & typeof app, calls };
}
const flush = async () => { await act(async () => { await Promise.resolve(); await Promise.resolve(); }); };
const render = async (el: JSX.Element) => { await act(async () => { root.render(el); }); await flush(); };
const type = (input: HTMLInputElement, v: string) => act(() => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, v); input.dispatchEvent(new Event('input', { bubbles: true })); });
const click = (el: Element) => act(() => { (el as HTMLElement).click(); });
const q = <T extends Element>(sel: string) => host.querySelector(sel) as T | null;
const btn = (re: RegExp) => [...host.querySelectorAll('button')].find((b) => re.test(b.textContent ?? ''))!;

describe('code de lancement : forme', () => {
  it('accepte 6 à 8 chiffres seulement', () => {
    for (const ok of ['482916', '4829160', '48291607']) expect(CODE_RE.test(ok)).toBe(true);
    for (const bad of ['', '12345', '123456789', '12345a', '12 3456']) expect(CODE_RE.test(bad)).toBe(false);
  });
  it('extrait l\'identifiant d\'une demande d\'arrêt', () => {
    expect(stopIdOf('http://127.0.0.1:7878/stop/stp_abc123')).toBe('stp_abc123');
    expect(stopIdOf('stp_abc')).toBe('stp_abc');
  });
});

describe('LaunchConfirm', () => {
  it('affiche résumé, températures, profil officiel, photo, champ de code et mise en garde', async () => {
    const { app } = mkApp({ get_launch_view_state: () => sc({ ...STATE, photo: { mime: 'image/jpeg', data: 'AAAA', taken_at: new Date().toISOString() } }) });
    await render(<LaunchConfirm app={app} first={CONF} pollMs={60_000} />);
    const t = host.textContent ?? '';
    expect(t).toContain('boite.gcode.3mf'); expect(t).toContain('Atelier (P1S)'); expect(t).toContain('1re couche 220');
    expect(t).toContain('profil officiel ✓'); expect(t).toContain('95 min');
    expect(q<HTMLImageElement>('figure img')?.src).toContain('data:image/jpeg;base64,AAAA');
    expect(q('input[type=password]')).not.toBeNull(); expect(q('input[type=checkbox]')).not.toBeNull();
    expect(t).toContain('Ne tapez jamais ce code dans la conversation');
  });

  it('affiche la mise à niveau du plateau, l\'heure locale, le risque de décollement et reprend une photo fraîche à la demande', async () => {
    const conf: ConfirmationView = { ...CONF, expires_at_local: '21:03 (08/11/2026, Europe/Paris)',
      binding: { ...CONF.binding, bed_leveling: false, adhesion_warnings: ['Risque de décollement ÉLEVÉ : pièce de 204 mm'] } };
    const { app, calls } = mkApp({ get_launch_view_state: () => sc({ ...STATE, photo: { mime: 'image/jpeg', data: 'AAAA', taken_at: new Date().toISOString(), taken_at_local: '20:55 (08/11/2026, Europe/Paris)', age_seconds: 12 } }) });
    await render(<LaunchConfirm app={app} first={conf} pollMs={60_000} />);
    const t = host.textContent ?? '';
    expect(t).toContain('Mise à niveau du plateau'); expect(t).toContain('non (désactivée pour cette impression)');
    expect(t).toContain('21:03 (08/11/2026, Europe/Paris)'); expect(t).toContain('il y a 12 s');
    expect(t).toContain('Risque de décollement'); expect(t).toContain('204 mm');
    expect(calls[0].arguments.fresh_photo).toBeUndefined(); // ouverture : le serveur peut réutiliser une photo de moins de 15 s
    await act(async () => { btn(/Reprendre la photo/).click(); await Promise.resolve(); });
    await flush();
    expect(calls.some((c) => c.name === 'get_launch_view_state' && c.arguments.fresh_photo === true && c.arguments.with_photo === true)).toBe(true);
  });

  it('montre en rouge un profil non officiel', async () => {
    const bad = { ...CONF, safety: { ...CONF.safety!, gcode_official: false, gcode_profile_line: 'G-code de démarrage : DIFFÉRENT du profil officiel ✗ (machine_start_gcode)', anomalies: ['G-code de démarrage : DIFFÉRENT du profil officiel ✗ (machine_start_gcode)'] } };
    const { app } = mkApp({ get_launch_view_state: () => sc(STATE) });
    await render(<LaunchConfirm app={app} first={bad} pollMs={60_000} />);
    expect(host.querySelector('[role=alert]')?.textContent).toContain('DIFFÉRENT du profil officiel');
  });

  it('désactive Lancer tant que le plateau n\'est pas coché et le code n\'a pas 6 à 8 chiffres', async () => {
    const { app } = mkApp({ get_launch_view_state: () => sc(STATE) });
    await render(<LaunchConfirm app={app} first={CONF} pollMs={60_000} />);
    const go = btn(/Lancer/) as HTMLButtonElement; const input = q<HTMLInputElement>('input[type=password]')!; const chk = q<HTMLInputElement>('input[type=checkbox]')!;
    expect(go.disabled).toBe(true);
    type(input, '4829'); click(chk); expect(go.disabled).toBe(true);
    type(input, '48291a6'); expect(input.value).toBe('482916'); // les lettres sont filtrées
    expect(go.disabled).toBe(false);
    click(chk); expect(go.disabled).toBe(true);
  });

  it('envoie le code à approve_print_with_code, efface le champ, n\'écrit le code nulle part et affiche l\'accord', async () => {
    const { app, calls } = mkApp({
      get_launch_view_state: () => sc(STATE),
      approve_print_with_code: () => sc({ approved: true, attempts_left: 3, locked: false, seconds_left: 120, message: 'Accord enregistré pour cette impression uniquement, valable 2 minutes.' }),
    });
    await render(<LaunchConfirm app={app} first={CONF} pollMs={60_000} />);
    click(q('input[type=checkbox]')!); type(q('input[type=password]')!, '482916');
    await act(async () => { click(btn(/Lancer/)); }); await flush();
    const call = calls.find((c) => c.name === 'approve_print_with_code')!;
    expect(call.arguments).toEqual({ confirmation_id: 'cf_1', code: '482916', plate_clear: true });
    expect(host.innerHTML).not.toContain('482916'); // ni dans le DOM, ni dans une valeur
    expect(host.textContent).toContain('Accord enregistré');
    expect(q('input[type=password]')).toBeNull();
    // jamais dans le contexte du modèle ni dans un message
    expect(app.updateModelContext).not.toHaveBeenCalled(); expect(app.sendMessage).not.toHaveBeenCalled(); expect(app.openLink).not.toHaveBeenCalled();
    // les autres appels n'ont pas reçu le code
    expect(JSON.stringify(calls.filter((c) => c.name !== 'approve_print_with_code'))).not.toContain('482916');
  });

  it('mauvais code : vide le champ, indique les essais restants, ne montre pas l\'accord', async () => {
    const { app } = mkApp({
      get_launch_view_state: () => sc(STATE),
      approve_print_with_code: () => sc({ approved: false, problem: 'bad_code', attempts_left: 2, locked: false, message: 'code incorrect (essais restants avant verrouillage : 2)' }),
    });
    await render(<LaunchConfirm app={app} first={CONF} pollMs={60_000} />);
    click(q('input[type=checkbox]')!); type(q('input[type=password]')!, '135791');
    await act(async () => { click(btn(/Lancer/)); }); await flush();
    expect(q<HTMLInputElement>('input[type=password]')!.value).toBe('');
    expect(host.textContent).toContain('Code incorrect.'); expect(host.textContent).toContain('2');
    expect(host.textContent).not.toContain('Accord enregistré');
    expect(host.innerHTML).not.toContain('135791');
  });

  it('3e échec : verrouillé, le champ disparaît, la page du code et le lien navigateur restent proposés', async () => {
    const { app } = mkApp({
      get_launch_view_state: () => sc(STATE),
      approve_print_with_code: () => sc({ approved: false, problem: 'bad_code', attempts_left: 0, locked: true, message: 'x' }),
    });
    await render(<LaunchConfirm app={app} first={CONF} pollMs={60_000} />);
    click(q('input[type=checkbox]')!); type(q('input[type=password]')!, '135791');
    await act(async () => { click(btn(/Lancer/)); }); await flush();
    expect(q('input[type=password]')).toBeNull();
    expect(host.textContent).toContain('verrouillée'); expect(host.textContent).toContain('/setup/launch-code');
    expect(btn(/Ouvrir la page de confirmation/)).toBeTruthy();
  });

  it('verrouillé dès le départ ou sans code défini : lien navigateur, pas de champ de code', async () => {
    for (const st of [{ ...STATE, locked: true, attempts_left: 0 }, { ...STATE, code_set: false }]) {
      const { app } = mkApp({ get_launch_view_state: () => sc(st) });
      await render(<LaunchConfirm app={app} first={CONF} pollMs={60_000} />);
      expect(q('input[type=password]')).toBeNull();
      const open = btn(/Ouvrir la page de confirmation/); expect(open).toBeTruthy();
      await act(async () => { click(open); });
      expect(app.openLink).toHaveBeenCalledWith({ url: CONF.confirm_url });
      act(() => root.render(<></>));
    }
  });

  it('sans code défini : dit clairement de le définir sur /setup/launch-code, propose cette page et le lien navigateur de secours', async () => {
    const { app } = mkApp({ get_launch_view_state: () => sc({ ...STATE, code_set: false }) });
    await render(<LaunchConfirm app={app} first={CONF} pollMs={60_000} />);
    expect(host.textContent).toContain('Définissez votre code de lancement sur');
    expect(host.textContent).toContain('http://127.0.0.1:7878/setup/launch-code');
    expect(q('input[type=password]')).toBeNull();
    await act(async () => { click(btn(/Ouvrir la page du code de lancement/)); });
    expect(app.openLink).toHaveBeenCalledWith({ url: STATE.setup_url });
    expect(btn(/Ouvrir la page de confirmation/)).toBeTruthy();
    // les consignes destinées au modèle ne sont jamais affichées à l'humain
    expect(host.textContent).not.toContain('ne le lui demande pas');
  });

  it('état introuvable (outil absent) : retombe sur le lien navigateur', async () => {
    const { app } = mkApp({ get_launch_view_state: () => { throw new Error('absent'); } });
    await render(<LaunchConfirm app={app} first={CONF} pollMs={60_000} />);
    expect(q('input[type=password]')).toBeNull(); expect(btn(/Ouvrir la page de confirmation/)).toBeTruthy();
  });

  it('confirmation déjà approuvée ou expirée', async () => {
    const { app } = mkApp({ get_launch_view_state: () => sc({ ...STATE, approved: true }) });
    await render(<LaunchConfirm app={app} first={CONF} pollMs={60_000} />);
    expect(host.textContent).toContain('Accord enregistré'); expect(q('input[type=password]')).toBeNull();
    act(() => root.render(<></>));
    const old = { ...CONF, expires_at: new Date(Date.now() - 1000).toISOString() };
    await render(<LaunchConfirm app={app} first={{ ...old, approved: false }} pollMs={60_000} />);
    // l'état renvoie approved=true : l'accord prime ; sans cela la confirmation expirée est signalée
    const { app: app2 } = mkApp({ get_launch_view_state: () => sc(STATE) });
    act(() => root.render(<></>));
    await render(<LaunchConfirm app={app2} first={old} pollMs={60_000} />);
    expect(host.textContent).toContain('expirée');
  });

  it('le serveur refuse (erreur d\'outil) : message sans le code', async () => {
    const { app } = mkApp({
      get_launch_view_state: () => sc(STATE),
      approve_print_with_code: () => ({ isError: true, content: [{ type: 'text', text: 'confirmation expirée' }] }),
    });
    await render(<LaunchConfirm app={app} first={CONF} pollMs={60_000} />);
    click(q('input[type=checkbox]')!); type(q('input[type=password]')!, '482916');
    await act(async () => { click(btn(/Lancer/)); }); await flush();
    expect(host.textContent).toContain('confirmation expirée'); expect(host.innerHTML).not.toContain('482916');
  });
});

describe('StopConfirm', () => {
  const STOP = { stop: { stop_id: 'stp_abc', print_job_id: 'pj_1', file: 'boite.gcode.3mf', printer_name: 'Atelier', percent: 42, layer: 10, total_layers: 24, expires_at: new Date(Date.now() + 600_000).toISOString() }, stop_url: 'http://127.0.0.1:7878/stop/stp_abc', code_set: true, locked: false, attempts_left: 3, setup_url: 'x' };

  it('arrête avec le code, sans case plateau, et efface le champ', async () => {
    const { app, calls } = mkApp({
      get_stop_view_state: () => sc(STOP),
      approve_stop_with_code: () => sc({ approved: true, attempts_left: 3, locked: false, message: "La commande d'arrêt a été acceptée par l'imprimante." }),
    });
    await render(<StopConfirm app={app} stopUrl="http://127.0.0.1:7878/stop/stp_abc" question="Veux-tu vraiment arrêter ?" pollMs={60_000} />);
    expect(host.textContent).toContain('42 %'); expect(q('input[type=checkbox]')).toBeNull(); expect(host.textContent).toContain('Ne tapez jamais ce code');
    type(q('input[type=password]')!, '482916');
    await act(async () => { click(btn(/Arrêter l'impression/)); }); await flush();
    expect(calls.find((c) => c.name === 'approve_stop_with_code')!.arguments).toEqual({ stop_id: 'stp_abc', code: '482916' });
    expect(host.textContent).toContain('Impression arrêtée'); expect(host.innerHTML).not.toContain('482916');
  });

  it('verrouillé ou sans code : page d\'arrêt du navigateur', async () => {
    for (const st of [{ ...STOP, locked: true }, { ...STOP, code_set: false }]) {
      const { app } = mkApp({ get_stop_view_state: () => sc(st) });
      await render(<StopConfirm app={app} stopUrl={STOP.stop_url} pollMs={60_000} />);
      expect(q('input[type=password]')).toBeNull(); expect(btn(/Ouvrir la page d'arrêt/)).toBeTruthy();
      act(() => root.render(<></>));
    }
  });

  it('demande expirée', async () => {
    const { app } = mkApp({ get_stop_view_state: () => ({ isError: true, content: [{ type: 'text', text: 'expirée' }] }) });
    await render(<StopConfirm app={app} stopUrl={STOP.stop_url} pollMs={60_000} />);
    expect(host.textContent).toContain('expirée');
  });
});
