import { useState } from 'react';
import { AlertTriangle, CheckCircle2, ShieldAlert } from 'lucide-react';

/**
 * Vue du test de résistance (ui://bambu/strength.html, liée à assess_strength et get_strength_report) : carte de
 * contraintes PNG (jointe au résultat de l'outil, aucune requête réseau), verdict, marge, point critique, entrées avec
 * leur provenance (donnée / hypothèse / défaut), recommandations et ce qui est mesuré, calculé ou estimé.
 * Lecture seule : la vue ne lance aucun calcul.
 */

export interface StrengthInput { name: string; value: string; source: 'user' | 'assumed' | 'default' }
export interface StrengthResult {
  model_id?: string; version_id?: string; verdict: 'ok' | 'a_surveiller' | 'risque'; verdict_text: string;
  safety_factor_achieved: number; safety_factor_required: number; max_von_mises_mpa: number; allowable_xy_mpa: number;
  max_displacement_mm: number; critical_note?: string; based_on_assumptions?: boolean;
  inputs?: StrengthInput[]; recommendations?: string[]; warnings?: string[]; measured?: string[]; calculated?: string[]; estimated?: string[];
  disclaimer?: string;
}
export interface StrengthImage { data: string; mime: string }
export interface StrengthView { result: StrengthResult; testId?: string; images: StrengthImage[] }

/** Un résultat d'assess_strength (`result`) ou de get_strength_report (`detail.result`). */
export function extractStrength(s: unknown): StrengthResult | undefined {
  const o = s as { result?: StrengthResult; detail?: { result?: StrengthResult } } | undefined;
  const r = o?.result ?? o?.detail?.result;
  return r && typeof r.verdict === 'string' && typeof r.safety_factor_achieved === 'number' ? r : undefined;
}

const SRC: Record<string, string> = { user: 'donnée', assumed: 'hypothèse', default: 'par défaut' };
const fx = (v: number) => `×${(Math.round(v * 10) / 10).toString().replace('.', ',')}`;
const fmt1 = (v: number) => (Math.round(v * 10) / 10).toString().replace('.', ',');

export function StrengthCard({ view }: { view: StrengthView }) {
  const r = view.result;
  const [big, setBig] = useState(false);
  const tone = r.verdict === 'ok' ? 'ok' : r.verdict === 'a_surveiller' ? 'warn' : 'err';
  const img = view.images[0];
  return (
    <>
      <header className="head">
        <h1>Test de résistance</h1>
        <span className="mono dim">{view.testId ?? ''}</span>
      </header>
      <p className={`str-verdict str-${tone}`} role="status">
        {r.verdict === 'ok' ? <CheckCircle2 size={16} aria-hidden /> : r.verdict === 'a_surveiller' ? <AlertTriangle size={16} aria-hidden /> : <ShieldAlert size={16} aria-hidden />}
        <span>{r.verdict === 'ok' ? `Tient avec une marge de ${fx(r.safety_factor_achieved)}` : r.verdict === 'a_surveiller' ? `À surveiller : marge de ${fx(r.safety_factor_achieved)}` : `Risque de casse (marge ${fx(r.safety_factor_achieved)})`}</span>
      </p>
      {r.based_on_assumptions && <p className="str-assumed" role="note"><AlertTriangle size={14} aria-hidden /> Résultat basé sur des hypothèses : à confirmer ou corriger avec l'humain.</p>}
      {img && (
        <figure className="plate">
          <button type="button" className="str-img" aria-pressed={big} aria-label="Carte de contraintes : cliquer pour agrandir ou réduire" onClick={() => setBig((b) => !b)}>
            <img src={`data:${img.mime};base64,${img.data}`} alt={`Carte de contraintes, contrainte max ${fmt1(r.max_von_mises_mpa)} MPa`} style={{ maxWidth: big ? 'none' : '100%' }} />
          </button>
          <figcaption className="small dim">Contrainte de von Mises en MPa (bleu faible, jaune moyenne, rouge forte) ; charges et appuis annotés.</figcaption>
        </figure>
      )}
      <dl className="dims mono">
        <div><dt>Contrainte max</dt><dd>{fmt1(r.max_von_mises_mpa)} MPa</dd></div>
        <div><dt>Limite admissible</dt><dd>{fmt1(r.allowable_xy_mpa)} MPa</dd></div>
        <div><dt>Marge visée</dt><dd>{fx(r.safety_factor_required)}</dd></div>
        <div><dt>Déplacement max</dt><dd>{(Math.round(r.max_displacement_mm * 100) / 100).toString().replace('.', ',')} mm</dd></div>
      </dl>
      {r.critical_note && <p className="small">Point critique : {r.critical_note}.</p>}
      {r.recommendations && r.recommendations.length > 0 && (
        <section aria-label="Recommandations"><div className="sub-label">Recommandations</div><ul className="str-list">{r.recommendations.map((x, i) => <li key={i}>{x}</li>)}</ul></section>
      )}
      {r.inputs && r.inputs.length > 0 && (
        <section aria-label="Entrées du calcul"><div className="sub-label">Entrées</div>
          <ul className="str-list">{r.inputs.map((m, i) => <li key={i}><span className={`tag${m.source === 'user' ? '' : ' tag-warn'}`}>{SRC[m.source] ?? m.source}</span> {m.name} : {m.value}</li>)}</ul>
        </section>
      )}
      {r.warnings && r.warnings.length > 0 && <ul className="str-list">{r.warnings.map((w, i) => <li key={i} className="dim">Attention : {w}</li>)}</ul>}
      <details className="small"><summary>Mesuré, calculé, estimé</summary>
        <p><strong>Mesuré</strong> : {(r.measured ?? []).join(' ; ')}</p>
        <p><strong>Calculé</strong> : {(r.calculated ?? []).join(' ; ')}</p>
        <p><strong>Estimé</strong> : {(r.estimated ?? []).join(' ; ')}</p>
      </details>
      <p className="foot">{r.disclaimer ?? 'Calcul simplifié : il ne remplace pas un essai réel.'}</p>
    </>
  );
}
