import type { ColorMode, LayerGeom } from './gcodeData';

/** Vues prédéfinies de la visionneuse. */
export type GcodeView = 'iso' | 'dessus' | 'face';

export interface GcodeRendererOptions {
  /** Couleur CSS (#rrggbb) de chaque type, dans l'ordre de features. */
  featureColors: string[];
  /** Emprise des extrusions [x0,y0,x1,y1] (mm) et hauteur totale : cadrage de la caméra. */
  bboxMM: [number, number, number, number];
  heightMM: number;
  /** Nombre de points annoncé : décide tubes (≤ limite) ou traits. */
  expectedPoints: number;
}

/** Ce que la visionneuse demande à la scène 3D (three.js dans la réalité, un faux dans les tests). */
export interface GcodeRenderer {
  readonly mode: 'tubes' | 'lines';
  /** Ajoute des couches décodées (affichage progressif). */
  addLayers(layers: LayerGeom[]): void;
  /** Couches min..max (à partir de 1, incluses) ; partial = part de la couche max déjà tracée (0..1). */
  setRange(min: number, max: number, partial: number): void;
  /** Types masqués (index dans features). */
  setHidden(hidden: ReadonlySet<number>): void;
  setColorMode(mode: ColorMode): void;
  setView(view: GcodeView): void;
  dispose(): void;
}

export type RendererFactory = (host: HTMLElement, o: GcodeRendererOptions) => GcodeRenderer | Promise<GcodeRenderer>;
