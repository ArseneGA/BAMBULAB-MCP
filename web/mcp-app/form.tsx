import { useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { AlertTriangle, Bell, CheckCircle2, ExternalLink, Palette, Pencil, Printer, Shapes, Target, Wallet } from '@shared/icons';
import { Badge, Button, Card, CheckGroup, ColorField, Notice, NumberField, SelectField, Segmented, TextField, Toggle } from '@shared/components';
import {
  buildArgs, groupFields, initialValues, modelNote, nothingChanged, resultOf, validate,
  type FormField, type FormImage, type FormLink, type FormResult, type FormSpec, type Values,
} from './formLogic';
import './form.css';

/**
 * Carte-formulaire générique (docs/research/mcp-apps.md § 12) : un outil visible du modèle, appelé sans ses champs,
 * renvoie `structuredContent.form` ; l'humain complète ici, la carte appelle l'outil réservé `spec.submit_tool`
 * (même validation serveur que l'outil normal), affiche le résultat puis informe le modèle (ui/update-model-context).
 *
 * SÉCURITÉ : aucun champ de type secret n'existe (le serveur refuse d'en émettre) ; la carte n'envoie que les champs
 * qu'elle affiche ; elle ne lance, n'arrête ni ne reprend aucune impression. Le code d'accès LAN se saisit sur la page
 * locale dont la carte affiche le lien après l'ajout.
 */

export interface AppLike {
  callServerTool(p: { name: string; arguments: Record<string, unknown> }): Promise<unknown>;
  openLink?(p: { url: string }): Promise<unknown>;
  updateModelContext?(p: { content: Array<{ type: 'text'; text: string }> }): Promise<unknown>;
}
interface ToolResultLike { structuredContent?: unknown; content?: Array<{ type: string; text?: string }>; isError?: boolean }
const textOf = (r: ToolResultLike) => (r.content ?? []).filter((c) => c.type === 'text').map((c) => c.text ?? '').join('\n').trim();

const ICONS: Record<string, ReactNode> = {
  add_printer: <Printer size={16} />, set_cost_settings: <Wallet size={16} />, configure_notifications: <Bell size={16} />,
  update_spool: <Palette size={16} />, set_manufacturing_goals: <Target size={16} />, create_from_template: <Shapes size={16} />,
};

const imgSrc = (i: FormImage) => `data:${i.mime};base64,${i.data}`;

function LinkButton({ app, link }: { app: AppLike; link: FormLink }) {
  const open = (e: React.MouseEvent) => {
    if (app.openLink) { e.preventDefault(); app.openLink({ url: link.url }).catch(() => { window.open(link.url, '_blank', 'noopener'); }); }
  };
  return (
    <a className="btn btn-secondary" href={link.url} target="_blank" rel="noopener noreferrer" onClick={open} title={link.note}>
      <ExternalLink size={14} aria-hidden />{link.label}
    </a>
  );
}

function FieldView({ f, value, error, onChange }: { f: FormField; value: unknown; error?: string; onChange: (v: unknown) => void }) {
  const hint = f.hint;
  switch (f.kind) {
    case 'number':
      return <NumberField label={f.label} value={typeof value === 'number' ? value : value === undefined || value === '' ? undefined : Number(value)} onChange={onChange} unit={f.unit} min={f.min} max={f.max} step={f.step ?? 'any'} hint={hint} error={error} required={f.required} name={f.name} />;
    case 'select': {
      const opts = f.options ?? [];
      return <SelectField label={f.label} value={value === undefined || value === null ? '' : String(value)} onChange={(v) => onChange(f.numeric && v !== '' ? Number(v) : v)} options={opts.map((o) => ({ value: o.value, label: o.label }))} hint={hint} error={error} required={f.required} placeholder={f.required ? 'Choisir…' : undefined} />;
    }
    case 'segmented': {
      const opts = f.options ?? [];
      return (
        <div className={`fld${error ? ' fld-err' : ''}`}>
          <span className="fld-label" id={`seg-${f.name}`}>{f.label}{f.unit && <span className="fld-unit"> ({f.unit})</span>}</span>
          <Segmented label={f.label} value={String(value ?? '')} onChange={(v) => onChange(f.numeric ? Number(v) : v)} options={opts.map((o) => ({ value: o.value, label: o.label }))} />
          {(error || hint) && <span className={error ? 'fld-msg fld-msg-err' : 'fld-msg'} role={error ? 'alert' : undefined}>{error ?? hint}</span>}
        </div>
      );
    }
    case 'color':
      return <ColorField label={f.label} value={typeof value === 'string' ? value : ''} onChange={onChange} hint={hint} error={error} presets={(f.options ?? []).map((o) => o.value)} />;
    case 'toggle':
      return <div className="fld"><Toggle checked={value === true} onChange={onChange} label={f.label} />{hint && <span className="fld-msg">{hint}</span>}</div>;
    case 'checks':
      return <CheckGroup label={f.label} options={(f.options ?? []).map((o) => ({ value: o.value, label: o.label, hint: o.hint }))} values={Array.isArray(value) ? (value as string[]) : []} onChange={onChange} hint={hint} />;
    case 'textarea':
      return (
        <div className={`fld${error ? ' fld-err' : ''}`}>
          <label className="fld-label" htmlFor={`f-${f.name}`}>{f.label}{f.required && <span className="fld-req" aria-hidden> *</span>}</label>
          <textarea id={`f-${f.name}`} className="input" rows={3} value={typeof value === 'string' ? value : ''} placeholder={f.placeholder} maxLength={f.max_length || undefined} aria-invalid={error ? true : undefined} onChange={(e) => onChange(e.target.value)} />
          {(error || hint) && <span className={error ? 'fld-msg fld-msg-err' : 'fld-msg'} role={error ? 'alert' : undefined}>{error ?? hint}</span>}
        </div>
      );
    default:
      return <TextField label={f.label} value={typeof value === 'string' || typeof value === 'number' ? String(value) : ''} onChange={(e) => onChange(e.target.value)} placeholder={f.placeholder} maxLength={f.max_length || undefined} hint={hint} error={error} required={f.required} name={f.name} autoComplete="off" />;
  }
}

export function ResultView({ app, spec, result, onEdit }: { app: AppLike; spec?: FormSpec; result: FormResult; onEdit?: () => void }) {
  return (
    <Card tone={result.ok ? 'ok' : 'err'} icon={result.ok ? <CheckCircle2 size={16} /> : <AlertTriangle size={16} />} title={result.title} meta={<Badge tone={result.ok ? 'ok' : 'err'}>{result.ok ? 'Fait' : 'Échec'}</Badge>} className="form-card"
      footer={(result.links?.length || onEdit) ? <>{onEdit && <Button variant="ghost" icon={<Pencil size={14} aria-hidden />} onClick={onEdit}>Modifier</Button>}{result.links?.map((l) => <LinkButton key={l.url} app={app} link={l} />)}</> : undefined}>
      <p className="form-summary" role="status">{result.summary}</p>
      {result.image && <figure className="form-image"><img src={imgSrc(result.image)} alt={result.image.alt ?? 'Aperçu'} /></figure>}
      {result.details && result.details.length > 0 && <dl className="kv">{result.details.map((d) => <div key={d.label}><dt>{d.label}</dt><dd>{d.value}</dd></div>)}</dl>}
      {result.notes?.filter(Boolean).map((n) => <Notice key={n} tone="neutral">{n}</Notice>)}
      {spec && <span className="sr-only">{spec.title}</span>}
    </Card>
  );
}

export function FormCard({ app, spec, previewDebounceMs = 400 }: { app: AppLike; spec: FormSpec; previewDebounceMs?: number }) {
  const [values, setValues] = useState<Values>(() => initialValues(spec));
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [serverError, setServerError] = useState<string>();
  const [info, setInfo] = useState<string>();
  const [result, setResult] = useState<FormResult>();
  const [preview, setPreview] = useState<FormImage | undefined>(spec.preview);
  const [previewBusy, setPreviewBusy] = useState(false);
  const reset = useRef(spec); // un nouveau résultat d'outil remplace le formulaire
  useEffect(() => { if (reset.current !== spec) { reset.current = spec; setValues(initialValues(spec)); setErrors({}); setResult(undefined); setServerError(undefined); setPreview(spec.preview); } }, [spec]);

  const set = (name: string, v: unknown) => { setValues((o) => ({ ...o, [name]: v })); setErrors((e) => { if (!(name in e)) return e; const n = { ...e }; delete n[name]; return n; }); setInfo(undefined); };

  const loadPreview = async () => {
    if (!spec.preview_tool) return;
    setPreviewBusy(true);
    try {
      const args = buildArgs({ ...spec, send: 'all' }, values) as Record<string, unknown>;
      const r = await app.callServerTool({ name: spec.preview_tool, arguments: { template: args.template, params: args.params ?? {} } }) as ToolResultLike;
      const img = (r.structuredContent as { image?: FormImage } | undefined)?.image;
      if (!r.isError && img) setPreview(img);
    } catch { /* l'aperçu est facultatif */ } finally { setPreviewBusy(false); }
  };
  // aperçu initial et mise à jour (temporisée) quand les paramètres changent
  const paramsKey = JSON.stringify(Object.entries(values).filter(([k]) => k.startsWith('params.')));
  useEffect(() => {
    if (!spec.preview_tool || result) return;
    const t = window.setTimeout(() => { void loadPreview(); }, previewDebounceMs);
    return () => window.clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [paramsKey, spec.preview_tool, result]);

  const submit = async () => {
    setServerError(undefined); setInfo(undefined);
    const errs = validate(spec, values);
    setErrors(errs);
    if (Object.keys(errs).length > 0) { setInfo('Complète les champs signalés.'); return; }
    if (nothingChanged(spec, values)) { setInfo('Aucun changement à enregistrer.'); return; }
    setBusy(true);
    try {
      const r = await app.callServerTool({ name: spec.submit_tool, arguments: buildArgs(spec, values) }) as ToolResultLike;
      if (r.isError) { setServerError(textOf(r) || "L'enregistrement a été refusé."); return; }
      const res = resultOf(r.structuredContent) ?? { ok: true, title: spec.title, summary: textOf(r) || 'Enregistré.' };
      setResult(res);
      app.updateModelContext?.({ content: [{ type: 'text', text: modelNote(spec, res) }] })?.catch?.(() => { /* hôte sans mise à jour de contexte */ });
    } catch (err) { setServerError((err as Error).message || "L'enregistrement a échoué."); } finally { setBusy(false); }
  };

  if (result) return <ResultView app={app} spec={spec} result={result} onEdit={spec.send === 'changed' || spec.form_id === 'add_printer' ? undefined : () => setResult(undefined)} />;

  const groups = groupFields(spec.fields);
  const missing = (spec.missing ?? []).filter((m) => spec.fields.some((f) => f.name === m)).map((m) => spec.fields.find((f) => f.name === m)!.label);
  const noFields = groups.length === 0;
  return (
    <Card icon={ICONS[spec.form_id] ?? <Pencil size={16} />} title={spec.title} className="form-card"
      footer={<>
        {spec.links?.map((l) => <LinkButton key={l.url} app={app} link={l} />)}
        {!noFields && <Button variant="primary" onClick={() => void submit()} disabled={busy} aria-busy={busy}>{busy ? 'Enregistrement…' : spec.submit_label}</Button>}
      </>}>
      {spec.intro && <p className="form-intro">{spec.intro}</p>}
      {missing.length > 0 && <p className="form-missing" role="status">À compléter : <strong>{missing.join(', ')}</strong></p>}
      {spec.preview_tool && (
        <figure className="form-image form-preview" aria-busy={previewBusy}>
          {preview ? <img src={imgSrc(preview)} alt={preview.alt ?? 'Aperçu'} /> : <div className="form-preview-empty">{previewBusy ? 'Aperçu en cours…' : 'Aperçu indisponible'}</div>}
        </figure>
      )}
      {noFields ? <Notice tone="neutral" title="Rien à régler ici">Aucun élément à modifier dans cette carte.</Notice> : (
        <form id={`form-${spec.form_id}`} className="form-body" noValidate
          // L'iframe de l'hôte est sandboxée : sans « allow-forms » l'envoi natif d'un formulaire est bloqué. Bouton et touche Entrée passent donc par ce code, jamais par submit.
          onSubmit={(e) => { e.preventDefault(); void submit(); }}
          onKeyDown={(e) => { if (e.key === 'Enter' && !busy && (e.target as HTMLElement).tagName === 'INPUT') { e.preventDefault(); void submit(); } }}>
          {groups.map((g, i) => (
            <fieldset key={`${g.title ?? ''}-${i}`} className="form-group">
              {g.title && <legend className="form-legend">{g.title}</legend>}
              <div className="form-grid">
                {g.fields.map((f) => <div key={f.name} className={f.kind === 'textarea' || f.kind === 'checks' || f.kind === 'segmented' || f.kind === 'color' ? 'form-wide' : undefined}><FieldView f={f} value={values[f.name]} error={errors[f.name]} onChange={(v) => set(f.name, v)} /></div>)}
              </div>
            </fieldset>
          ))}
        </form>
      )}
      {spec.notice && <Notice tone="neutral">{spec.notice}</Notice>}
      {info && <p className="form-info" role="status">{info}</p>}
      {serverError && <Notice tone="err" title="Refusé">{serverError}</Notice>}
    </Card>
  );
}
