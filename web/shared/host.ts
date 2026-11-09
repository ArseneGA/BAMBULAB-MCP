/**
 * Thème des cartes MCP App : elles suivent l'hôte (Claude), clair ou sombre, sans jamais perdre la charte verte.
 *
 *  1. `ctx.theme` ('light' | 'dark') annoncé par l'hôte (ui/initialize puis ui/notifications/host-context-changed) ;
 *  2. sinon, la luminance de `ctx.styles.variables['--color-background-primary']` quand l'hôte la fournit ;
 *  3. sinon `prefers-color-scheme` du navigateur.
 *
 * Les variables de l'hôte ne sont PAS posées telles quelles sur :root (elles écraseraient --font-sans, --text-1…
 * de la charte) : elles sont recopiées sous le préfixe `--host-` (ex. `--host-color-background-primary`), disponibles
 * pour qui voudrait s'y raccorder. La police reste DM Sans embarquée.
 */
export type ThemeName = 'light' | 'dark';

export interface HostContextLike {
  theme?: string;
  styles?: { variables?: Record<string, string | undefined> };
}

const asTheme = (t: unknown): ThemeName | undefined => (t === 'light' || t === 'dark' ? t : undefined);

/** Luminance relative approchée d'une couleur #rgb / #rrggbb / rgb() ; undefined si illisible. */
export function luminanceOf(color: string | undefined): number | undefined {
  if (!color) return undefined;
  let r: number, g: number, b: number;
  const hex = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(color.trim());
  if (hex) {
    const h = hex[1].length === 3 ? hex[1].split('').map((c) => c + c).join('') : hex[1];
    r = parseInt(h.slice(0, 2), 16); g = parseInt(h.slice(2, 4), 16); b = parseInt(h.slice(4, 6), 16);
  } else {
    const m = /^rgba?\(\s*(\d+)[\s,]+(\d+)[\s,]+(\d+)/i.exec(color.trim());
    if (!m) return undefined;
    r = +m[1]; g = +m[2]; b = +m[3];
  }
  return (0.2126 * r + 0.7152 * g + 0.0722 * b) / 255;
}

/** Thème déduit d'un contexte d'hôte, ou undefined quand il ne dit rien d'exploitable. */
export function themeFromHost(ctx?: HostContextLike): ThemeName | undefined {
  const direct = asTheme(ctx?.theme);
  if (direct) return direct;
  const lum = luminanceOf(ctx?.styles?.variables?.['--color-background-primary']);
  return lum === undefined ? undefined : lum < 0.45 ? 'dark' : 'light';
}

export const systemTheme = (): ThemeName =>
  typeof window !== 'undefined' && window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';

/** Pose le thème sur <html> ; renvoie celui qui a été appliqué. */
export function applyTheme(t: ThemeName, root: HTMLElement = document.documentElement): ThemeName {
  root.dataset.theme = t;
  root.style.colorScheme = t;
  return t;
}

/**
 * Applique le contexte de l'hôte (thème + variables préfixées) ; renvoie le thème posé, ou undefined quand le contexte
 * ne dit rien du thème (une notification partielle ne doit pas le réinitialiser : le thème en cours est conservé).
 */
export function applyHostContext(ctx?: HostContextLike, root: HTMLElement = document.documentElement): ThemeName | undefined {
  const vars = ctx?.styles?.variables;
  if (vars) for (const [k, v] of Object.entries(vars)) if (typeof v === 'string' && v !== '') root.style.setProperty(`--host-${k.replace(/^--/, '')}`, v);
  const t = themeFromHost(ctx);
  return t ? applyTheme(t, root) : undefined;
}

/** Suit prefers-color-scheme tant que l'hôte n'a rien annoncé ; renvoie la fonction d'arrêt. */
export function followSystemTheme(isHostDriven: () => boolean, root: HTMLElement = document.documentElement): () => void {
  applyTheme(systemTheme(), root);
  const mq = typeof window !== 'undefined' ? window.matchMedia?.('(prefers-color-scheme: dark)') : undefined;
  if (!mq) return () => {};
  const on = () => { if (!isHostDriven()) applyTheme(mq.matches ? 'dark' : 'light', root); };
  mq.addEventListener?.('change', on);
  return () => mq.removeEventListener?.('change', on);
}
