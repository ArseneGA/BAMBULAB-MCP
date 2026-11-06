/**
 * DONNÉES DE DÉMONSTRATION — fictives, aucune ne provient d'un vrai catalogue,
 * d'une bobine mesurée ou d'une imprimante. Toute valeur `null` est INCONNUE
 * (jamais remplacée silencieusement par une valeur d'une autre référence).
 */
export const DEMO = true as const;

export type FileFormat = 'STL' | 'STEP' | '3MF';
export type SyncState = 'copied-linux' | 'windows-only';

export interface DemoVersion { id: string; label: string; date: string; note: string; sha256Short: string }
export interface DemoFile { name: string; format: FileFormat; sizeKB: number; sync: SyncState }
export interface DemoModel {
  id: string;
  demo: true;
  name: string;
  collection: string;
  tags: string[];
  files: DemoFile[];
  /** mm : X, Y, Z */
  dims: { x: number; y: number; z: number };
  versions: DemoVersion[];
  sync: SyncState;
  geometry: 'box' | 'cylinder' | 'torus' | 'cone' | 'hollow';
}

const v = (id: string, label: string, date: string, note: string, sha: string): DemoVersion => ({ id, label, date, note, sha256Short: sha });

export const demoModels: DemoModel[] = [
  { id: 'm01', demo: true, name: 'Pince de maintien 25 mm', collection: 'Atelier', tags: ['pince', 'fonctionnel'], files: [{ name: 'pince_25_v3.step', format: 'STEP', sizeKB: 412, sync: 'copied-linux' }, { name: 'pince_25_v3.stl', format: 'STL', sizeKB: 1840, sync: 'copied-linux' }], dims: { x: 48, y: 32, z: 20 }, versions: [v('v3', 'v3', '2026-10-28', 'Renfort de la charnière', '9f2c1a'), v('v2', 'v2', '2026-10-12', 'Jeu porté à 0,3 mm', '4be07d'), v('v1', 'v1', '2026-09-30', 'Première version', 'c81d55')], sync: 'copied-linux', geometry: 'hollow' },
  { id: 'm02', demo: true, name: 'Support de manette', collection: 'Maison', tags: ['support', 'esthétique'], files: [{ name: 'support_manette.3mf', format: '3MF', sizeKB: 2210, sync: 'copied-linux' }], dims: { x: 110, y: 90, z: 74 }, versions: [v('v2', 'v2', '2026-10-20', 'Base alourdie', '1a77e0'), v('v1', 'v1', '2026-10-05', 'Première version', 'e33b92')], sync: 'copied-linux', geometry: 'box' },
  { id: 'm03', demo: true, name: 'Boîtier capteur étanche', collection: 'Électronique', tags: ['boîtier', 'extérieur'], files: [{ name: 'boitier_capteur.step', format: 'STEP', sizeKB: 986, sync: 'windows-only' }, { name: 'boitier_capteur.stl', format: 'STL', sizeKB: 3120, sync: 'windows-only' }], dims: { x: 64, y: 44, z: 28 }, versions: [v('v1', 'v1', '2026-10-30', 'Indexé depuis Windows', '7d0f42')], sync: 'windows-only', geometry: 'box' },
  { id: 'm04', demo: true, name: 'Joint souple de bouchon', collection: 'Atelier', tags: ['joint', 'souple'], files: [{ name: 'joint_bouchon.stl', format: 'STL', sizeKB: 540, sync: 'copied-linux' }], dims: { x: 38, y: 38, z: 4 }, versions: [v('v1', 'v1', '2026-09-18', 'Première version', 'b20c9e')], sync: 'copied-linux', geometry: 'torus' },
  { id: 'm05', demo: true, name: 'Engrenage module 1,5', collection: 'Mécanique', tags: ['engrenage', 'emboîtement'], files: [{ name: 'engrenage_m15.step', format: 'STEP', sizeKB: 233, sync: 'copied-linux' }, { name: 'engrenage_m15.3mf', format: '3MF', sizeKB: 880, sync: 'copied-linux' }], dims: { x: 42, y: 42, z: 10 }, versions: [v('v4', 'v4', '2026-11-01', 'Jeu de denture ajusté', '5c4a18'), v('v3', 'v3', '2026-10-22', 'Moyeu agrandi', 'a6e931'), v('v2', 'v2', '2026-10-10', 'Dents recalculées', '08fd2b'), v('v1', 'v1', '2026-10-01', 'Première version', 'd71e60')], sync: 'copied-linux', geometry: 'cylinder' },
  { id: 'm06', demo: true, name: 'Pied de table réglable', collection: 'Maison', tags: ['pied', 'grande pièce'], files: [{ name: 'pied_table.stl', format: 'STL', sizeKB: 4720, sync: 'windows-only' }], dims: { x: 60, y: 60, z: 85 }, versions: [v('v1', 'v1', '2026-10-27', 'Indexé depuis Windows', '3e9b77')], sync: 'windows-only', geometry: 'cone' },
  { id: 'm07', demo: true, name: 'Guide de câble modulaire', collection: 'Électronique', tags: ['câble', 'modulaire'], files: [{ name: 'guide_cable.3mf', format: '3MF', sizeKB: 301, sync: 'copied-linux' }], dims: { x: 30, y: 18, z: 12 }, versions: [v('v2', 'v2', '2026-10-15', 'Pas de 6 mm', 'f08a14'), v('v1', 'v1', '2026-10-02', 'Première version', '62bc03')], sync: 'copied-linux', geometry: 'box' },
  { id: 'm08', demo: true, name: 'Diffuseur lumineux', collection: 'Maison', tags: ['diffusion', 'esthétique'], files: [{ name: 'diffuseur.step', format: 'STEP', sizeKB: 157, sync: 'copied-linux' }, { name: 'diffuseur.stl', format: 'STL', sizeKB: 960, sync: 'copied-linux' }], dims: { x: 80, y: 80, z: 52 }, versions: [v('v1', 'v1', '2026-10-24', 'Première version', '9a31dc')], sync: 'copied-linux', geometry: 'cone' },
  { id: 'm09', demo: true, name: 'Poignée de tiroir', collection: 'Atelier', tags: ['poignée', 'fonctionnel'], files: [{ name: 'poignee_tiroir.stl', format: 'STL', sizeKB: 720, sync: 'windows-only' }], dims: { x: 96, y: 22, z: 30 }, versions: [v('v1', 'v1', '2026-10-29', 'Indexé depuis Windows', 'ee5026')], sync: 'windows-only', geometry: 'torus' },
  { id: 'm10', demo: true, name: 'Entretoise d\'axe 8 mm', collection: 'Mécanique', tags: ['entretoise', 'emboîtement'], files: [{ name: 'entretoise_8.step', format: 'STEP', sizeKB: 64, sync: 'copied-linux' }, { name: 'entretoise_8.stl', format: 'STL', sizeKB: 210, sync: 'copied-linux' }], dims: { x: 16, y: 16, z: 24 }, versions: [v('v2', 'v2', '2026-10-17', 'Chanfrein ajouté', '2d84a7'), v('v1', 'v1', '2026-10-03', 'Première version', 'bb19f5')], sync: 'copied-linux', geometry: 'cylinder' },
  { id: 'm11', demo: true, name: 'Capot de ventilateur 40 mm', collection: 'Électronique', tags: ['capot', 'ventilation'], files: [{ name: 'capot_fan40.3mf', format: '3MF', sizeKB: 1305, sync: 'copied-linux' }], dims: { x: 40, y: 40, z: 9 }, versions: [v('v1', 'v1', '2026-10-21', 'Première version', '71c0e8')], sync: 'copied-linux', geometry: 'hollow' },
  { id: 'm12', demo: true, name: 'Clip de plan de travail', collection: 'Atelier', tags: ['pince', 'grande pièce'], files: [{ name: 'clip_plan.stl', format: 'STL', sizeKB: 2890, sync: 'windows-only' }], dims: { x: 120, y: 40, z: 36 }, versions: [v('v1', 'v1', '2026-10-31', 'Indexé depuis Windows', 'c4d712')], sync: 'windows-only', geometry: 'box' },
];

export const collections = Array.from(new Set(demoModels.map((m) => m.collection)));
export const allTags = Array.from(new Set(demoModels.flatMap((m) => m.tags))).sort();

/* ---------- Matériaux ---------- */
/** null = inconnu, affiché comme tel dans l'interface. */
export interface DemoMaterial {
  id: string; demo: true;
  family: 'PLA' | 'PETG' | 'ASA' | 'TPU' | 'PA-CF';
  maker: string | null;
  reference: string | null;
  colour: string | null;
  diameterMm: number | null;
  nozzleMinMm: number | null;
  nozzleTempC: [number, number] | null;
  bedTempC: [number, number] | null;
  enclosureAdvised: boolean | null;
  hardenedNozzleRequired: boolean | null;
  p1sCompatibility: 'à vérifier' | 'compatible déclaré' | 'inconnu';
  spoolState: string | null;
  calibrations: Array<{ date: string; note: string }>;
}
export const demoMaterials: DemoMaterial[] = [
  { id: 'mat1', demo: true, family: 'PLA', maker: 'Fabricant démo A', reference: 'PLA-DEMO-01', colour: 'Gris clair', diameterMm: 1.75, nozzleMinMm: 0.4, nozzleTempC: [190, 220], bedTempC: [45, 60], enclosureAdvised: false, hardenedNozzleRequired: false, p1sCompatibility: 'compatible déclaré', spoolState: 'Séchée, ouverte le 2026-10-02', calibrations: [{ date: '2026-10-05', note: 'Débit calibré' }] },
  { id: 'mat2', demo: true, family: 'PETG', maker: 'Fabricant démo B', reference: 'PETG-DEMO-02', colour: 'Naturel', diameterMm: 1.75, nozzleMinMm: 0.4, nozzleTempC: [230, 250], bedTempC: [70, 80], enclosureAdvised: null, hardenedNozzleRequired: false, p1sCompatibility: 'à vérifier', spoolState: null, calibrations: [] },
  { id: 'mat3', demo: true, family: 'ASA', maker: null, reference: null, colour: 'Noir', diameterMm: 1.75, nozzleMinMm: 0.4, nozzleTempC: [240, 260], bedTempC: null, enclosureAdvised: true, hardenedNozzleRequired: false, p1sCompatibility: 'à vérifier', spoolState: 'Neuve, scellée', calibrations: [] },
  { id: 'mat4', demo: true, family: 'TPU', maker: 'Fabricant démo C', reference: 'TPU-DEMO-95A', colour: 'Rouge', diameterMm: 1.75, nozzleMinMm: 0.4, nozzleTempC: [210, 230], bedTempC: [30, 45], enclosureAdvised: null, hardenedNozzleRequired: false, p1sCompatibility: 'inconnu', spoolState: null, calibrations: [] },
  { id: 'mat5', demo: true, family: 'PA-CF', maker: 'Fabricant démo D', reference: null, colour: null, diameterMm: 1.75, nozzleMinMm: null, nozzleTempC: null, bedTempC: null, enclosureAdvised: true, hardenedNozzleRequired: true, p1sCompatibility: 'inconnu', spoolState: null, calibrations: [] },
];

/* ---------- Imprimante ---------- */
export interface DemoPrinter {
  id: string; demo: true; model: 'P1S'; name: string;
  firmware: string | null; nozzle: { diameterMm: number; type: string } | null; plate: string | null; ams: string | null;
  state: 'idle-demo'; reachable: boolean; plateVerifiedEmpty: boolean | null;
}
export const demoPrinter: DemoPrinter = {
  id: 'p1s-demo', demo: true, model: 'P1S', name: 'P1S (démo)',
  firmware: null, nozzle: { diameterMm: 0.4, type: 'acier inoxydable' }, plate: null, ams: null,
  state: 'idle-demo', reachable: true, plateVerifiedEmpty: null,
};

/* ---------- Disponibilité (simulée) ---------- */
export const demoAvailability = {
  linux: 'online' as const,
  windowsAgent: 'offline' as const,
  printer: 'online' as const,
  storageFreeGB: 412,
};
