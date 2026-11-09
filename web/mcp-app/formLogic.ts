/**
 * Logique pure des cartes-formulaires (voir internal/mcpserver/forms.go) : types du contrat serveur, pré-remplissage,
 * validation, construction des arguments de l'outil de soumission. Aucune I/O, testée seule (form.test.tsx).
 */

export interface FormOption { value: string; label: string; hint?: string; color?: string }
export type FieldKind = 'text' | 'textarea' | 'number' | 'select' | 'segmented' | 'color' | 'toggle' | 'checks' | 'hidden';
export interface FormField {
  name: string; label: string; kind: FieldKind; required?: boolean; unit?: string; min?: number; max?: number; step?: number; integer?: boolean; numeric?: boolean;
  options?: FormOption[]; placeholder?: string; hint?: string; group?: string; max_length?: number; pattern?: string;
}
export interface FormLink { label: string; url: string; note?: string }
export interface FormImage { mime: string; data: string; alt?: string }
export interface FormSpec {
  form_id: string; title: string; intro?: string; tool: string; submit_tool: string; submit_label: string; send: 'all' | 'changed';
  fields: FormField[]; values?: Record<string, unknown>; missing?: string[]; links?: FormLink[]; notice?: string; preview_tool?: string; preview?: FormImage;
}
export interface FormResult {
  ok: boolean; title: string; summary: string; details?: Array<{ label: string; value: string }>; links?: FormLink[]; image?: FormImage; notes?: string[];
}

type Rec = Record<string, unknown>;
const isRec = (v: unknown): v is Rec => typeof v === 'object' && v !== null && !Array.isArray(v);

/** Reconnaît une carte-formulaire dans le contenu structuré d'un résultat d'outil. */
export function formOf(sc: unknown): FormSpec | undefined {
  if (!isRec(sc) || !isRec(sc.form)) return undefined;
  const f = sc.form;
  if (typeof f.form_id !== 'string' || typeof f.submit_tool !== 'string' || !Array.isArray(f.fields)) return undefined;
  return f as unknown as FormSpec;
}

/** Reconnaît le résumé d'une action effectuée (outil appelé avec tous ses champs, ou soumission). */
export function resultOf(sc: unknown): FormResult | undefined {
  if (!isRec(sc) || !isRec(sc.form_result) || typeof sc.form_result.title !== 'string') return undefined;
  return sc.form_result as unknown as FormResult;
}

export type Values = Record<string, unknown>;

/** Valeurs de départ : celles du serveur (ce que le modèle savait), sans écraser un champ par undefined. */
export function initialValues(spec: FormSpec): Values {
  const v: Values = { ...(spec.values ?? {}) };
  for (const f of spec.fields) {
    if (!(f.name in v) || v[f.name] === null) {
      if (f.kind === 'toggle') v[f.name] = false;
      else if (f.kind === 'checks') v[f.name] = [];
      else delete v[f.name];
    }
    if ((f.kind === 'select' || f.kind === 'segmented') && f.numeric && typeof v[f.name] === 'string' && v[f.name] !== '') v[f.name] = Number(v[f.name]);
  }
  return v;
}

const blank = (v: unknown) => v === undefined || v === null || (typeof v === 'string' && v.trim() === '') || (typeof v === 'number' && Number.isNaN(v));
const HEX = /^#[0-9a-fA-F]{6}$/;

/** Erreurs par nom de champ ({} = valide). La validation définitive reste celle du serveur. */
export function validate(spec: FormSpec, values: Values): Record<string, string> {
  const errs: Record<string, string> = {};
  for (const f of spec.fields) {
    if (f.kind === 'hidden') continue;
    const v = values[f.name];
    if (f.required && blank(v)) { errs[f.name] = 'Champ obligatoire.'; continue; }
    if (blank(v)) continue;
    if (f.kind === 'number') {
      const n = Number(v);
      if (!Number.isFinite(n)) errs[f.name] = 'Nombre attendu.';
      else if (f.integer && !Number.isInteger(n)) errs[f.name] = 'Nombre entier attendu.';
      else if (f.min !== undefined && n < f.min) errs[f.name] = `Au moins ${f.min}${f.unit ? ` ${f.unit}` : ''}.`;
      else if (f.max !== undefined && n > f.max) errs[f.name] = `Au plus ${f.max}${f.unit ? ` ${f.unit}` : ''}.`;
    } else if (f.kind === 'color') {
      if (!HEX.test(String(v))) errs[f.name] = 'Couleur au format #RRGGBB.';
    } else if ((f.kind === 'text' || f.kind === 'textarea') && typeof v === 'string') {
      if (f.pattern) { try { if (!new RegExp(f.pattern).test(v.trim())) errs[f.name] = f.hint ? `Format invalide. ${f.hint}` : 'Format invalide.'; } catch { /* motif illisible : le serveur tranche */ } }
      if (f.max_length && v.length > f.max_length) errs[f.name] = `${f.max_length} caractères au plus.`;
    }
  }
  return errs;
}

/** Pose une valeur à un chemin « a.b.0.c » : les segments numériques créent des tableaux. */
export function setPath(obj: Rec, path: string, value: unknown): void {
  const parts = path.split('.');
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  let cur: any = obj;
  parts.forEach((p, i) => {
    const key = /^\d+$/.test(p) && Array.isArray(cur) ? Number(p) : p;
    if (i === parts.length - 1) { cur[key] = value; return; }
    if (typeof cur[key] !== 'object' || cur[key] === null) cur[key] = /^\d+$/.test(parts[i + 1]) ? [] : {};
    cur = cur[key];
  });
}

const same = (a: unknown, b: unknown) => JSON.stringify(a ?? null) === JSON.stringify(b ?? null);

/**
 * Arguments de l'outil de soumission.
 *  - send = all : tous les champs renseignés (un champ vide est omis, un interrupteur vaut toujours vrai ou faux) ;
 *  - send = changed : seulement les champs modifiés par l'humain ;
 *  - les champs hidden (identifiants, noms de canaux…) sont toujours joints.
 * Aucun champ hors du formulaire n'est jamais envoyé : la carte ne peut pas transmettre autre chose que ce qu'elle affiche.
 */
export function buildArgs(spec: FormSpec, values: Values): Rec {
  const out: Rec = {};
  const init = initialValues(spec);
  for (const f of spec.fields) {
    const v = values[f.name];
    if (f.kind === 'hidden') { if (!blank(v)) setPath(out, f.name, v); continue; }
    if (spec.send === 'changed' && same(v, init[f.name])) continue;
    if (f.kind === 'toggle') { setPath(out, f.name, v === true); continue; }
    if (f.kind === 'checks') { setPath(out, f.name, Array.isArray(v) ? v : []); continue; }
    if (blank(v)) continue;
    if (f.kind === 'number' || ((f.kind === 'select' || f.kind === 'segmented') && f.numeric)) setPath(out, f.name, Number(v));
    else setPath(out, f.name, typeof v === 'string' ? v.trim() : v);
  }
  return out;
}

/** Vrai quand « changed » et que l'humain n'a rien modifié (rien à envoyer). */
export function nothingChanged(spec: FormSpec, values: Values): boolean {
  if (spec.send !== 'changed') return false;
  const init = initialValues(spec);
  return spec.fields.every((f) => f.kind === 'hidden' || same(values[f.name], init[f.name]));
}

/** Groupes consécutifs de champs visibles (même `group`) pour les sections de la carte. */
export function groupFields(fields: FormField[]): Array<{ title?: string; fields: FormField[] }> {
  const out: Array<{ title?: string; fields: FormField[] }> = [];
  for (const f of fields) {
    if (f.kind === 'hidden') continue;
    const last = out[out.length - 1];
    if (last && last.title === f.group) last.fields.push(f);
    else out.push({ title: f.group, fields: [f] });
  }
  return out;
}

/** Phrase donnée au modèle de langage quand l'humain valide la carte (aucun secret : jamais les valeurs saisies). */
export function modelNote(spec: FormSpec, r: FormResult): string {
  return `Dans la carte « ${spec.title} », l'utilisateur a validé le formulaire : ${r.summary}${r.notes && r.notes.length ? ' ' + r.notes.join(' ') : ''}`;
}
