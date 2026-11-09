import { useMemo } from 'react';
import { GcodeViewer } from '../src/viewer/gcode/GcodeViewer';
import { isGcodeIndex, isGcodeSlice } from '../src/viewer/gcode/gcodeData';
import type { GcodeSource } from '../src/viewer/gcode/gcodeData';
import type { RendererFactory } from '../src/viewer/gcode/gcodeRenderer';

/**
 * Vue ui://bambu/gcode.html (liée à show_gcode_preview) : visionneuse 3D couche par couche. Les données viennent des
 * outils réservés à la vue get_gcode_layers (résumé par couche) et get_gcode_slice (tranches de ≤ 12 000 points) : la
 * vue n'ouvre aucune connexion et ne lit aucun fichier.
 */

export interface AppLike {
  callServerTool(p: { name: string; arguments: Record<string, unknown> }): Promise<unknown>;
}
interface ToolResultLike { structuredContent?: unknown; content?: Array<{ type: string; text?: string }>; isError?: boolean }

export interface GcodePreviewInfo {
  kind: 'gcode_preview'; artifact_sha256: string; plate: number; layers_total: number; features?: string[];
  total_time_s?: number; time_source?: string; filament_mm?: number;
}

export function isGcodePreview(s: unknown): s is GcodePreviewInfo {
  const o = s as Partial<GcodePreviewInfo> | undefined;
  return !!o && o.kind === 'gcode_preview' && typeof o.artifact_sha256 === 'string' && typeof o.plate === 'number';
}

const fail = (r: ToolResultLike, dflt: string) => new Error((r.content ?? []).map((c) => c.text ?? '').join(' ').trim() || dflt);

/** Source de données par outils MCP. */
export function mcpGcodeSource(app: AppLike, info: Pick<GcodePreviewInfo, 'artifact_sha256' | 'plate'>): GcodeSource {
  const base = { artifact_id: info.artifact_sha256, plate: info.plate };
  return {
    async index() {
      const r = (await app.callServerTool({ name: 'get_gcode_layers', arguments: base })) as ToolResultLike | undefined;
      if (r?.isError) throw fail(r, 'Résumé des couches refusé');
      if (!isGcodeIndex(r?.structuredContent)) throw new Error('Réponse de get_gcode_layers invalide');
      return r.structuredContent;
    },
    async slice(fromLayer, withTravel) {
      const r = (await app.callServerTool({ name: 'get_gcode_slice', arguments: { ...base, from_layer: fromLayer, with_travel: withTravel } })) as ToolResultLike | undefined;
      if (r?.isError) throw fail(r, 'Tranche refusée');
      if (!isGcodeSlice(r?.structuredContent)) throw new Error('Réponse de get_gcode_slice invalide');
      return r.structuredContent;
    },
  };
}

export function GcodePreviewCard({ app, info, createRenderer }: { app: AppLike; info: GcodePreviewInfo; createRenderer?: RendererFactory }) {
  const source = useMemo(() => mcpGcodeSource(app, info), [app, info.artifact_sha256, info.plate]); // eslint-disable-line react-hooks/exhaustive-deps
  return <GcodeViewer source={source} createRenderer={createRenderer} compact title="Découpe, couche par couche" />;
}
