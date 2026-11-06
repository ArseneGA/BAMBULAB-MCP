import { ExtrudeGeometry, LatheGeometry, Matrix4, Path, Shape, Vector2 } from 'three';
import type { BufferGeometry } from 'three';
import type { DemoModel } from '../data/demo';

/**
 * Géométries de démonstration générées localement (aucun fichier chargé).
 * Repère imprimante : X/Y sur le plateau, Z vers le haut, pièce posée sur z = 0 et centrée en X/Y.
 * Pas de CSG : les perçages sont des trous de Shape extrudée, le reste est composé de sous-parties.
 */
export interface Part { id: string; label: string; geometry: BufferGeometry }
type Dims = DemoModel['dims'];
type Kind = DemoModel['geometry'];

const TAU = Math.PI * 2;

function rrect(w: number, h: number, r: number): Shape {
  const s = new Shape(); const x = -w / 2; const y = -h / 2; const k = Math.min(r, w / 2 - 0.01, h / 2 - 0.01);
  if (k <= 0) { s.moveTo(x, y); s.lineTo(x + w, y); s.lineTo(x + w, y + h); s.lineTo(x, y + h); s.closePath(); return s; }
  s.moveTo(x + k, y); s.lineTo(x + w - k, y); s.quadraticCurveTo(x + w, y, x + w, y + k);
  s.lineTo(x + w, y + h - k); s.quadraticCurveTo(x + w, y + h, x + w - k, y + h);
  s.lineTo(x + k, y + h); s.quadraticCurveTo(x, y + h, x, y + h - k);
  s.lineTo(x, y + k); s.quadraticCurveTo(x, y, x + k, y); return s;
}
function circle(r: number): Shape { const s = new Shape(); s.absarc(0, 0, r, 0, TAU, false); return s; }
function hole(s: Shape, cx: number, cy: number, r: number) { const p = new Path(); p.absarc(cx, cy, r, 0, TAU, true); s.holes.push(p); }
function rectHole(s: Shape, cx: number, cy: number, w: number, h: number) {
  const p = new Path(); p.moveTo(cx - w / 2, cy - h / 2); p.lineTo(cx - w / 2, cy + h / 2); p.lineTo(cx + w / 2, cy + h / 2); p.lineTo(cx + w / 2, cy - h / 2); p.closePath(); s.holes.push(p);
}
/** Extrusion de 0 à depth en Z, avec chanfrein b. */
function ext(shape: Shape, depth: number, b: number): ExtrudeGeometry {
  const bb = Math.min(b, depth / 2 - 0.01);
  if (bb <= 0.01) return new ExtrudeGeometry(shape, { depth, bevelEnabled: false, curveSegments: 20 });
  const g = new ExtrudeGeometry(shape, { depth: depth - 2 * bb, bevelEnabled: true, bevelSize: bb, bevelThickness: bb, bevelOffset: -bb, bevelSegments: 1, curveSegments: 20 });
  g.translate(0, 0, bb); return g;
}
/** Révolution autour de Z : profil (rayon, hauteur). */
function lathe(profile: Array<[number, number]>, seg = 48): LatheGeometry {
  const g = new LatheGeometry(profile.map(([r, h]) => new Vector2(r, h)), seg); g.rotateX(Math.PI / 2); return g;
}
/** Plan du profil dans XZ : la forme (u,v) devient (x,z), l'épaisseur s'étend en -y. */
function upright(g: BufferGeometry): BufferGeometry { g.rotateX(Math.PI / 2); return g; }

/** (u, v, profondeur) -> (profondeur, u, v) : le profil est dans le plan YZ, extrudé le long de X. */
function alongX(g: BufferGeometry): BufferGeometry { g.applyMatrix4(new Matrix4().set(0, 0, 1, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 1)); return g; }

export function buildParts(kind: Kind, d: Dims, age = 0): Part[] {
  const b = age === 0 ? 1 : age === 1 ? 0.5 : 0; // les anciennes révisions n'ont pas encore de chanfreins
  const hs = 1 - 0.15 * Math.min(age, 3);          // perçages plus petits sur les anciennes révisions
  const { x, y, z } = d;
  const parts: Part[] = [];
  const add = (id: string, label: string, geometry: BufferGeometry) => parts.push({ id, label, geometry });

  if (kind === 'box') {
    if (z >= 0.6 * x) { // support en L avec nervures
      const t = 6; const w = 8;
      const base = rrect(x, y, 6); [[-1, -1], [1, -1], [-1, 1], [1, 1]].forEach(([a, c]) => hole(base, (a * (x / 2 - 9)), (c * (y / 2 - 9)), 2.6 * hs));
      add('base', 'Embase', ext(base, t, b));
      const wall = rrect(x, z, 5); hole(wall, -x / 4, z * 0.18, 5 * hs); hole(wall, x / 4, z * 0.18, 5 * hs);
      const wg = upright(ext(wall, w, b)); wg.translate(0, y / 2, z / 2); add('montant', 'Montant', wg);
      [-1, 1].forEach((s, i) => {
        const rib = new Shape(); const run = y * 0.55; const hh = z * 0.7; rib.moveTo(0, 0); rib.lineTo(-run, 0); rib.lineTo(0, hh); rib.closePath();
        const g = alongX(ext(rib, 5, b * 0.5)); g.translate(s * x * 0.3 - 2.5, y / 2 - w, t); add(`nervure${i}`, `Nervure ${i + 1}`, g);
      });
    } else if (x >= 100 && y <= 40) { // clip en C extrudé le long de X
      const prof = new Shape(); const t = 5; const hh = z; const ww = y;
      prof.moveTo(-ww / 2, 0); prof.lineTo(ww / 2, 0); prof.lineTo(ww / 2, hh); prof.lineTo(ww / 2 - 14, hh); prof.lineTo(ww / 2 - 14, hh - t); prof.lineTo(ww / 2 - t, hh - t); prof.lineTo(ww / 2 - t, t); prof.lineTo(-ww / 2 + t, t); prof.lineTo(-ww / 2 + t, hh - t); prof.lineTo(-ww / 2 + 14, hh - t); prof.lineTo(-ww / 2 + 14, hh); prof.lineTo(-ww / 2, hh); prof.closePath();
      const g = alongX(ext(prof, x, b * 0.6)); g.translate(-x / 2, 0, 0); add('corps', 'Corps du clip', g);
      const boss = circle(6); hole(boss, 0, 0, 2.2 * hs); const bg = ext(boss, 3, b); bg.translate(0, 0, t); add('plot', 'Plot de vis', bg);
    } else if (x <= 35) { // guide de câble
      const body = rrect(x, y, 3); rectHole(body, 0, 0, x * 0.55, y * 0.42); hole(body, -x / 2 + 4, 0, 1.3 * hs); hole(body, x / 2 - 4, 0, 1.3 * hs);
      add('corps', 'Corps', ext(body, z, b));
    } else { // boîtier : coque + fond
      const wall = rrect(x, y, 6); const inner = new Path(); const k = 2.4; const iw = x - 2 * k; const ih = y - 2 * k;
      inner.moveTo(-iw / 2, -ih / 2); inner.lineTo(-iw / 2, ih / 2); inner.lineTo(iw / 2, ih / 2); inner.lineTo(iw / 2, -ih / 2); inner.closePath(); wall.holes.push(inner);
      const floor = ext(rrect(x, y, 6), 2.4, b);
      add('fond', 'Fond', floor);
      const shell = ext(wall, z - 2.4, b * 0.6); shell.translate(0, 0, 2.4); add('coque', 'Coque', shell);
      [[-1, -1], [1, -1], [-1, 1], [1, 1]].forEach(([a, c], i) => { const pp = circle(3.4); hole(pp, 0, 0, 1.3 * hs); const g = ext(pp, z - 4, 0); g.translate(a * (x / 2 - 6.5), c * (y / 2 - 6.5), 2.4); add(`plot${i}`, `Plot de vis ${i + 1}`, g); });
    }
  } else if (kind === 'hollow') {
    if (Math.abs(x - y) < 1) { // capot de ventilateur
      const plate = rrect(x, y, 5); hole(plate, 0, 0, x * 0.42 * (age ? 0.95 : 1)); [[-1, -1], [1, -1], [-1, 1], [1, 1]].forEach(([a, c]) => hole(plate, a * (x / 2 - 4), c * (y / 2 - 4), 1.7 * hs));
      add('plaque', 'Plaque', ext(plate, 2.2, b));
      const rim = rrect(x, y, 5); const ri = rrect(x - 3, y - 3, 3.5); rim.holes.push(new Path(ri.getPoints().reverse())); const rg = ext(rim, z - 2.2, b * 0.5); rg.translate(0, 0, 2.2); add('cadre', 'Cadre', rg);
      const hubS = circle(x * 0.14); hole(hubS, 0, 0, x * 0.06); add('moyeu', 'Moyeu central', ext(hubS, z - 3, 0));
      add('barre1', 'Barre 1', ext(rrect(x - 4, 2.4, 0), 2.2, 0)); const b2 = ext(rrect(2.4, y - 4, 0), 2.2, 0); add('barre2', 'Barre 2', b2);
    } else { // pince : corps en U + axe de charnière
      const w = x; const h = y; const t = 8; const slot = 12;
      const u = new Shape(); u.moveTo(-w / 2, -h / 2); u.lineTo(w / 2, -h / 2); u.lineTo(w / 2, -slot / 2); u.lineTo(-w / 2 + t + 3, -slot / 2); u.lineTo(-w / 2 + t + 3, slot / 2); u.lineTo(w / 2, slot / 2); u.lineTo(w / 2, h / 2); u.lineTo(-w / 2, h / 2); u.closePath();
      hole(u, -w / 2 + t / 2, h / 2 - 5, 2 * hs); hole(u, -w / 2 + t / 2, -h / 2 + 5, 2 * hs);
      add('corps', 'Corps en U', ext(u, z, b));
      const pin = ext(circle(3.2 * hs), z + 4, b * 0.5); pin.translate(w / 2 - 12, 0, -2); add('axe', 'Axe de charnière', pin);
    }
  } else if (kind === 'cylinder') {
    if (x > 30) { // engrenage
      const n = 22; const ro = x / 2; const rr = ro - 3.4; const s = new Shape();
      for (let i = 0; i < n; i++) {
        const a = (i / n) * TAU; const st = TAU / n;
        const pts: Array<[number, number]> = [[rr, a], [ro, a + st * 0.2], [ro, a + st * 0.42], [rr, a + st * 0.62]];
        pts.forEach(([r, ang], j) => { const px = Math.cos(ang) * r; const py = Math.sin(ang) * r; if (i === 0 && j === 0) s.moveTo(px, py); else s.lineTo(px, py); });
      }
      s.closePath(); hole(s, 0, 0, x * 0.2 * hs); for (let i = 0; i < 4; i++) { const a = (i / 4) * TAU + 0.4; hole(s, Math.cos(a) * x * 0.32, Math.sin(a) * x * 0.32, x * 0.06 * hs); }
      add('denture', 'Denture', ext(s, z, b * 0.5));
      const hub = circle(x * 0.28); hole(hub, 0, 0, x * 0.2 * hs); const hg = ext(hub, z + 3, b); add('moyeu', 'Moyeu', hg);
    } else { // entretoise chanfreinée
      const r = x / 2; const ri = Math.max(1, r * 0.5 * hs); const c = b * 1.2;
      add('corps', 'Corps', lathe([[ri, 0], [r - c, 0], [r, c], [r, z - c], [r - c, z], [ri, z]], 40));
    }
  } else if (kind === 'torus') {
    if (Math.abs(x - y) < 2) { // joint plat
      const ring = circle(x / 2); hole(ring, 0, 0, x * 0.32); add('joint', 'Joint', ext(ring, z, b * 0.6));
      const lip = circle(x * 0.36); hole(lip, 0, 0, x * 0.32); const lg = ext(lip, z * 1.9, 0); add('levre', 'Lèvre d\'étanchéité', lg);
    } else { // poignée : barre + 2 platines percées
      const barL = x - 2 * 8; const bar = rrect(barL, y, y / 2.4); const bg = ext(bar, 9, b); bg.translate(0, 0, z - 9); add('barre', 'Barre', bg);
      [-1, 1].forEach((sg, i) => {
        const post = rrect(10, y, 3); hole(post, 0, 0, 2.2 * hs);
        const g = ext(post, z - 6, b); g.translate(sg * (barL / 2 - 4), 0, 0); add(`pied${i}`, `Pied ${i + 1}`, g);
      });
    }
  } else { // cone
    const r = x / 2;
    if (z > x) { // pied réglable : embase, fût conique, tige
      const c = b * 1.5;
      add('embase', 'Embase', lathe([[0, 0], [r - c, 0], [r, c], [r, 6], [r * 0.7, 9], [0, 9]], 48));
      add('fut', 'Fût', lathe([[r * 0.7, 9], [r * 0.42, z * 0.55], [r * 0.34, z * 0.62], [0, z * 0.62], [0, 9]].map(([a, h]) => [a, h] as [number, number]), 40));
      add('tige', 'Tige filetée', lathe([[0, z * 0.62], [r * 0.17, z * 0.62], [r * 0.17, z - 1.5], [r * 0.14, z], [0, z]], 24));
    } else { // diffuseur : coque conique creuse + collerette
      const w = 2.2;
      const r0 = r * 0.46;
      add('coque', 'Coque', lathe([[r0, 0], [r0 + w, 0], [r, z], [r - w, z]].map(([a, h]) => [a, h] as [number, number]), 56));
      const col = circle(r * 0.62); hole(col, 0, 0, r0); add('collerette', 'Collerette', ext(col, 4, b));
    }
  }
  return parts;
}

export function partList(kind: Kind, d: Dims): Array<{ id: string; label: string }> {
  return buildParts(kind, d).map((p) => ({ id: p.id, label: p.label }));
}
