// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { GcodePreviewCard, isGcodePreview, mcpGcodeSource, type AppLike } from './gcode';
import { fakeFactory, fakeSource, mkIndex } from '../src/viewer/gcode/gcodeTestkit';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLElement; let root: Root;
beforeEach(() => { host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host); });
afterEach(() => { act(() => root.unmount()); host.remove(); });
const settle = async (n = 12) => { for (let i = 0; i < n; i++) await act(async () => { await Promise.resolve(); }); };

const INFO = { kind: 'gcode_preview' as const, artifact_sha256: 'a'.repeat(64), plate: 2, layers_total: 5 };

/** Hôte simulé : répond aux deux outils réservés à la vue à partir d'une source de test. */
function mkApp() {
  const src = fakeSource(5, 2);
  const calls: Array<{ name: string; arguments: Record<string, unknown> }> = [];
  const app: AppLike = {
    callServerTool: vi.fn(async (p) => {
      calls.push(p);
      if (p.name === 'get_gcode_layers') return { structuredContent: await src.index() };
      if (p.name === 'get_gcode_slice') return { structuredContent: await src.slice(p.arguments.from_layer as number, p.arguments.with_travel === true) };
      return { isError: true, content: [{ type: 'text', text: 'outil inconnu' }] };
    }),
  };
  return { app, calls };
}

describe('vue gcode.html (show_gcode_preview)', () => {
  it('reconnaît le résultat de show_gcode_preview', () => {
    expect(isGcodePreview(INFO)).toBe(true);
    expect(isGcodePreview({ kind: 'layers', artifact_sha256: 'x', plate: 1 })).toBe(false);
    expect(isGcodePreview({ kind: 'gcode_preview' })).toBe(false);
  });

  it('source MCP : outils réservés get_gcode_layers / get_gcode_slice avec empreinte, plaque, couche et déplacements', async () => {
    const { app, calls } = mkApp();
    const s = mcpGcodeSource(app, INFO);
    const ix = await s.index();
    expect(ix.layers_total).toBe(5);
    const sl = await s.slice(3, true);
    expect(sl.from).toBe(3);
    expect(calls[0]).toEqual({ name: 'get_gcode_layers', arguments: { artifact_id: INFO.artifact_sha256, plate: 2 } });
    expect(calls[1]).toEqual({ name: 'get_gcode_slice', arguments: { artifact_id: INFO.artifact_sha256, plate: 2, from_layer: 3, with_travel: true } });
  });

  it('erreurs de l\'outil et réponses invalides remontées', async () => {
    const bad: AppLike = { callServerTool: async () => ({ isError: true, content: [{ type: 'text', text: 'couche hors plage' }] }) };
    await expect(mcpGcodeSource(bad, INFO).index()).rejects.toThrow('couche hors plage');
    await expect(mcpGcodeSource(bad, INFO).slice(1, false)).rejects.toThrow('couche hors plage');
    const odd: AppLike = { callServerTool: async () => ({ structuredContent: { x: 1 } }) };
    await expect(mcpGcodeSource(odd, INFO).index()).rejects.toThrow('invalide');
    await expect(mcpGcodeSource(odd, INFO).slice(1, false)).rejects.toThrow('invalide');
  });

  it('la carte charge le résumé puis les tranches (chargement progressif) et affiche la légende', async () => {
    const { app, calls } = mkApp();
    const f = fakeFactory();
    await act(async () => { root.render(<GcodePreviewCard app={app} info={INFO} createRenderer={f.factory} />); });
    await settle();
    expect(calls.map((c) => c.name)).toEqual(['get_gcode_layers', 'get_gcode_slice', 'get_gcode_slice', 'get_gcode_slice']);
    expect(calls.slice(1).map((c) => c.arguments.from_layer)).toEqual([1, 3, 5]);
    expect(f.last.added).toEqual([1, 2, 3, 4, 5]);
    expect(host.textContent).toContain('Paroi extérieure');
    expect(host.textContent).toContain(`${mkIndex(5).layers_total} couches`);
  });
});
