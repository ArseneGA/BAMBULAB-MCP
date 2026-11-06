import { Box, Cone, Cylinder, Donut, PackageOpen } from 'lucide-react';
import type { DemoModel } from '../data/demo';

const icons = { box: Box, cylinder: Cylinder, torus: Donut, cone: Cone, hollow: PackageOpen } as const;

/** Vignette géométrique : icône de la famille de forme, pas d'image. */
export function Thumb({ geometry, size = 40 }: { geometry: DemoModel['geometry']; size?: number }) {
  const I = icons[geometry];
  return <span className="thumb" style={{ width: size, height: size }} aria-hidden><I size={Math.round(size * 0.5)} strokeWidth={1.5} /></span>;
}

export const norm = (s: string) => s.normalize('NFD').replace(/[̀-ͯ]/g, '').toLowerCase();
export const fmtSize = (kb: number) => (kb >= 1024 ? `${(kb / 1024).toFixed(1).replace('.', ',')} Mo` : `${kb} Ko`);
export const dimsLabel = (d: DemoModel['dims']) => `${d.x} × ${d.y} × ${d.z} mm`;
