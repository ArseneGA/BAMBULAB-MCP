import { useEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { AlertTriangle, Check, Info } from 'lucide-react';
import { Button, Field, Panel, Select, Tag, Toggle } from '../components';
import { demoAvailability, demoMaterials, demoPrinter } from '../data/demo';
import './materials.css';
import './print.css';

/* Machine d'états métier (dossier §10). Tout est SIMULÉ : aucun réseau. */
type St =
  | 'DRAFT' | 'PREPARED' | 'SLICING' | 'VALIDATED' | 'UPLOADING' | 'UPLOADED'
  | 'AWAITING_PRINT_CONFIRMATION' | 'START_REQUESTED' | 'PRINTING' | 'COMPLETED'
  | 'FAILED' | 'CANCELLED' | 'START_UNKNOWN';
const MAIN: St[] = ['DRAFT', 'PREPARED', 'SLICING', 'VALIDATED', 'UPLOADING', 'UPLOADED', 'AWAITING_PRINT_CONFIRMATION', 'START_REQUESTED', 'PRINTING', 'COMPLETED'];
const stLabel: Record<St, string> = {
  DRAFT: 'Brouillon', PREPARED: 'Préparé', SLICING: 'Slicing', VALIDATED: 'Validé', UPLOADING: 'Envoi', UPLOADED: 'Envoyé',
  AWAITING_PRINT_CONFIRMATION: 'Attente de confirmation', START_REQUESTED: 'Démarrage demandé', PRINTING: 'Impression', COMPLETED: 'Terminé',
  FAILED: 'Échec', CANCELLED: 'Annulé', START_UNKNOWN: 'Démarrage incertain',
};

const FILE = { name: 'pince_25_v3.3mf', sha: '9f2c1a7e04b8d3516c2e90a47f1b6d28e5c3a0974d1f8b62ae07c3d95b41e6f0', sizeMB: 2.2, dims: '48 × 32 × 20 mm' };
const TOTAL_LAYERS = 120;
const MIN_PER_LAYER = 1.1; // minutes simulées par couche
const plates = [{ value: 'textured-pei', label: 'Plateau PEI texturé (démo)' }, { value: 'smooth-pei', label: 'Plateau PEI lisse (démo)' }];
const plateLabel = (v: string) => plates.find((p) => p.value === v)?.label ?? v;

interface LogEntry { t: string; text: string }
interface S {
  status: St; plate: string; materialId: string; fileOnLinux: boolean; printerOk: boolean;
  armNetLoss: boolean; armFail: boolean;
  progress: number; layer: number;
  confirmation: { key: string; token: string } | null; emptyChecked: boolean;
  notice: { tone: 'warn' | 'err' | 'info'; text: string } | null; log: LogEntry[];
}
const initial: S = {
  status: 'DRAFT', plate: 'textured-pei', materialId: '', fileOnLinux: true, printerOk: true, armNetLoss: false, armFail: false,
  progress: 0, layer: 0, confirmation: null, emptyChecked: false, notice: null, log: [],
};
const clock = () => new Date().toLocaleTimeString('fr-FR');
const withLog = (s: S, text: string): LogEntry[] => [{ t: clock(), text }, ...s.log].slice(0, 40);
const to = (s: S, status: St, extra: Partial<S> = {}, note?: string): S => ({ ...s, ...extra, status, log: withLog(s, note ?? `${s.status} → ${status}`) });

interface Check { tone: 'ok' | 'warn' | 'err'; label: string; why: string }
function buildReport(s: S): Check[] {
  const m = demoMaterials.find((x) => x.id === s.materialId);
  const out: Check[] = [];
  out.push(s.fileOnLinux
    ? { tone: 'ok', label: 'Fichier présent sur le serveur Linux', why: 'Copie vérifiée par empreinte (démo).' }
    : { tone: 'err', label: 'Fichier disponible uniquement sur Windows', why: "Le PC Windows est éteint et le fichier n'est pas copié sur Linux. Rallume le PC ou importe le fichier avant de continuer." });
  out.push({ tone: 'ok', label: 'Géométrie fermée et lisible (démo)', why: 'Contrôle simulé, aucune analyse réelle n\'a eu lieu.' });
  out.push({ tone: 'ok', label: `Encombrement ${FILE.dims}`, why: 'Tient sur le plateau (cote de démonstration).' });
  if (!m) out.push({ tone: 'err', label: 'Aucune matière affectée', why: 'Choisis une matière dans « Paramètres de lancement ». Aucune n\'est prise par défaut.' });
  else if (m.p1sCompatibility !== 'compatible déclaré') out.push({ tone: 'warn', label: `Compatibilité P1S de ${m.family} : ${m.p1sCompatibility}`, why: 'Non démontrée. Tu peux continuer, la responsabilité du choix reste à toi.' });
  else out.push({ tone: 'ok', label: `Matière ${m.family} : compatibilité déclarée par le fabricant`, why: 'Déclaration, pas une vérification sur ta machine.' });
  out.push({ tone: 'warn', label: 'Firmware de la P1S inconnu', why: 'La voie d\'envoi et de commande ne peut pas être qualifiée tant qu\'il n\'est pas relevé.' });
  out.push({ tone: 'warn', label: 'Plateau vide : non vérifiable à distance', why: 'Une imprimante au repos ne prouve pas que le plateau est vide. Tu le confirmeras avant le lancement.' });
  return out;
}

export function PrintPage() {
  const [s, setS] = useState<S>(initial);
  const mat = demoMaterials.find((x) => x.id === s.materialId);
  const report = useMemo(() => buildReport(s), [s.materialId, s.fileOnLinux]); // eslint-disable-line react-hooks/exhaustive-deps
  const blockers = report.filter((c) => c.tone === 'err');
  const key = `${demoPrinter.id}|${FILE.sha}|${s.plate}|${s.materialId}`;
  const sRef = useRef(s); sRef.current = s;

  /* Simulation temporelle, pilotée par l'état. */
  useEffect(() => {
    const st = s.status;
    if (st === 'SLICING') {
      const id = window.setInterval(() => setS((p) => {
        if (p.status !== 'SLICING') return p;
        const n = Math.min(100, p.progress + 10);
        if (n < 100) return { ...p, progress: n };
        const errs = buildReport(p).filter((c) => c.tone === 'err');
        return errs.length
          ? to(p, 'PREPARED', { progress: 0, notice: { tone: 'err', text: `Validation refusée : ${errs.map((e) => e.label).join(' ; ')}.` } }, 'Slicing terminé, validation refusée')
          : to(p, 'VALIDATED', { progress: 100, notice: null }, 'Slicing terminé, rapport sans erreur bloquante');
      }), 160);
      return () => window.clearInterval(id);
    }
    if (st === 'UPLOADING') {
      const id = window.setInterval(() => setS((p) => {
        if (p.status !== 'UPLOADING') return p;
        if (!p.printerOk) return to(p, 'VALIDATED', { progress: 0, notice: { tone: 'err', text: 'Envoi interrompu : imprimante indisponible. Rien n\'a été lancé ; relance l\'envoi quand elle est joignable.' } }, 'Envoi interrompu, imprimante injoignable');
        const n = Math.min(100, p.progress + 8);
        if (n < 100) return { ...p, progress: n };
        return to(p, 'UPLOADED', { progress: 100 }, 'Fichier reçu par la P1S (aucun chauffage, aucun mouvement)');
      }), 180);
      return () => window.clearInterval(id);
    }
    if (st === 'START_REQUESTED') {
      const id = window.setTimeout(() => setS((p) => {
        if (p.status !== 'START_REQUESTED') return p;
        return p.armNetLoss
          ? to(p, 'START_UNKNOWN', { armNetLoss: false, notice: null }, 'Commande envoyée, puis perte réseau : aucun accusé')
          : to(p, 'PRINTING', { layer: 0 }, 'Commande acceptée, état PRINTING observé');
      }), 1400);
      return () => window.clearTimeout(id);
    }
    if (st === 'PRINTING') {
      const id = window.setInterval(() => setS((p) => {
        if (p.status !== 'PRINTING') return p;
        const n = p.layer + 1;
        if (p.armFail && n >= 38) return to(p, 'FAILED', { layer: n, armFail: false }, `Échec simulé à la couche ${n} (démo)`);
        if (n >= TOTAL_LAYERS) return to(p, 'COMPLETED', { layer: TOTAL_LAYERS, confirmation: null, emptyChecked: false }, 'Impression terminée');
        return { ...p, layer: n };
      }), 260);
      return () => window.clearInterval(id);
    }
  }, [s.status]);

  /* Changer un élément lié invalide la confirmation. */
  const change = (patch: Partial<Pick<S, 'plate' | 'materialId'>>, what: string) => setS((p) => {
    if (['START_REQUESTED', 'PRINTING', 'COMPLETED', 'FAILED', 'CANCELLED', 'START_UNKNOWN', 'UPLOADING'].includes(p.status)) return p;
    const next = { ...p, ...patch };
    const log = withLog(p, `${what} modifié`);
    if (p.status === 'AWAITING_PRINT_CONFIRMATION' || p.status === 'UPLOADED') {
      return { ...next, status: 'UPLOADED', confirmation: null, emptyChecked: false, log: [{ t: clock(), text: 'Confirmation invalidée' }, ...log].slice(0, 40), notice: { tone: 'warn', text: `Confirmation invalidée : ${what} a changé. Redemande la confirmation pour le lancer.` } };
    }
    if (p.status === 'SLICING' || p.status === 'VALIDATED') return { ...next, status: 'PREPARED', progress: 0, log, notice: { tone: 'warn', text: `${what} a changé : le slicing et la validation sont à refaire.` } };
    return { ...next, log };
  });

  const reset = () => setS({ ...initial, fileOnLinux: s.fileOnLinux, printerOk: s.printerOk, armNetLoss: s.armNetLoss, armFail: s.armFail, log: withLog(s, 'Nouveau brouillon : aucune autorisation précédente n\'est reprise') });
  const idx = MAIN.indexOf(s.status);
  const locked = ['UPLOADING', 'START_REQUESTED', 'PRINTING', 'COMPLETED', 'FAILED', 'CANCELLED', 'START_UNKNOWN'].includes(s.status);
  const confirmValid = s.confirmation?.key === key;
  const canStart = s.status === 'AWAITING_PRINT_CONFIRMATION' && s.emptyChecked && s.printerOk && !!mat;

  const startPrint = () => {
    if (!canStart) return;
    setS((p) => to(p, 'START_REQUESTED', { confirmation: { key, token: `cf-${FILE.sha.slice(0, 6)}-${Date.now().toString(36)}` } }, 'Confirmation explicite reçue, commande de démarrage envoyée (simulé)'));
  };

  const remaining = Math.round((TOTAL_LAYERS - s.layer) * MIN_PER_LAYER);
  const elapsed = Math.round(s.layer * MIN_PER_LAYER);
  const fmt = (m: number) => `${Math.floor(m / 60)} h ${String(m % 60).padStart(2, '0')}`;

  return (
    <>
      <div className="page-head"><span className="eyebrow">Espace</span><h1>Impression</h1><p>Parcours de lancement simulé : validation, envoi, confirmation explicite, suivi. Rien ne contacte l'imprimante ni le réseau.</p></div>
      <div className="pr-col">
        <nav aria-label="Étapes de l'impression">
          <ol className="pr-steps">
            {MAIN.map((m, i) => (
              <li key={m} data-s={idx > i ? 'done' : undefined} aria-current={s.status === m ? 'step' : undefined} title={stLabel[m]}>
                {idx > i && <Check size={11} aria-hidden />}{m}
              </li>
            ))}
          </ol>
          <p className="src-note" style={{ marginTop: 'var(--sp-2)' }}>
            État actuel : <strong className="mono">{s.status}</strong> ({stLabel[s.status]}){['FAILED', 'CANCELLED', 'START_UNKNOWN'].includes(s.status) && <> — sortie hors parcours nominal</>}
          </p>
        </nav>

        {s.notice && <div className={`notice notice-${s.notice.tone === 'warn' ? '' : s.notice.tone}`.trim()} role="status"><AlertTriangle size={16} aria-hidden style={{ flex: 'none' }} /><span>{s.notice.text}</span></div>}
        {!s.printerOk && !['PRINTING', 'COMPLETED', 'FAILED', 'CANCELLED', 'START_UNKNOWN', 'START_REQUESTED'].includes(s.status) && (
          <div className="notice notice-err" role="alert"><AlertTriangle size={16} aria-hidden style={{ flex: 'none' }} /><span><strong>Imprimante indisponible.</strong> L'envoi et le lancement sont bloqués. Vérifie qu'elle est allumée et sur le réseau, puis rétablis l'état dans les cas de démonstration.</span></div>
        )}
        {!s.fileOnLinux && <div className="notice notice-info"><Info size={16} aria-hidden style={{ flex: 'none' }} /><span>Agent Windows {demoAvailability.windowsAgent === 'offline' ? 'hors ligne' : 'éteint'} : le fichier n'existe que sur Windows et ne fait pas partie des capacités hors ligne.</span></div>}

        {s.status === 'AWAITING_PRINT_CONFIRMATION' && (
          <section className="confirm" role="dialog" aria-modal="false" aria-labelledby="confirm-title">
            <h2 id="confirm-title">Le fichier {FILE.name} est envoyé sur la P1S. Veux-tu lancer cette impression ?</h2>
            <dl className="kv">
              <dt>Imprimante</dt><dd>{demoPrinter.name}</dd>
              <dt>Empreinte du fichier</dt><dd className="mono">sha256 {FILE.sha.slice(0, 16)}…{FILE.sha.slice(-8)}</dd>
              <dt>Plateau</dt><dd>{plateLabel(s.plate)}</dd>
              <dt>Affectation matière</dt><dd>{mat ? `${mat.family} — ${mat.reference ?? 'référence inconnue'}` : <span className="unknown">aucune</span>}</dd>
            </dl>
            <p className="sim-note">Cette autorisation vaut pour cette impression uniquement. Si l'un de ces éléments change, elle est invalidée.</p>
            <label className="check">
              <input type="checkbox" checked={s.emptyChecked} onChange={(e) => setS((p) => ({ ...p, emptyChecked: e.target.checked }))} />
              <span>J'ai vérifié que le plateau est vide. L'imprimante au repos ne le prouve pas.</span>
            </label>
            {!s.printerOk && <p className="notice notice-err" role="alert">Imprimante indisponible : lancement impossible pour l'instant.</p>}
            <div className="row-wrap">
              <Button variant="primary" disabled={!canStart || !confirmValid && !!s.confirmation} onClick={startPrint}>Oui, lancer l'impression</Button>
              <Button variant="secondary" onClick={() => setS((p) => to(p, 'UPLOADED', { emptyChecked: false, notice: { tone: 'info', text: 'Lancement refusé. Le fichier reste sur la P1S ; rien n\'est démarré.' } }, 'Lancement refusé par l\'utilisateur'))}>Non, ne pas lancer</Button>
            </div>
          </section>
        )}

        {s.status === 'START_UNKNOWN' && (
          <section className="confirm" aria-labelledby="unk-title" style={{ borderColor: 'var(--warn)' }}>
            <h2 id="unk-title">Le démarrage est incertain</h2>
            <p>La commande a été émise, puis la liaison a été perdue avant tout accusé. L'impression a peut-être démarré. <strong>Ne relance pas</strong> avant d'avoir interrogé l'état de l'imprimante : le protocole ne garantit pas un envoi unique.</p>
            <p className="sim-note">Les deux boutons ci-dessous simulent le résultat de l'interrogation.</p>
            <div className="row-wrap">
              <Button variant="primary" onClick={() => setS((p) => to(p, 'PRINTING', { layer: 3, notice: { tone: 'info', text: 'Réconciliation : la P1S imprime déjà. Suivi repris.' } }, 'Réconciliation : état PRINTING observé'))}>Simuler : la P1S imprime</Button>
              <Button variant="secondary" onClick={() => setS((p) => to(p, 'UPLOADED', { confirmation: null, emptyChecked: false, notice: { tone: 'info', text: 'Réconciliation : la P1S est au repos, rien n\'a démarré. Une nouvelle confirmation est nécessaire.' } }, 'Réconciliation : imprimante au repos, confirmation à refaire'))}>Simuler : la P1S est au repos</Button>
            </div>
          </section>
        )}

        <div className="pr-layout">
          <div className="pr-col">
            <Panel title="Parcours" actions={<Tag>simulé</Tag>}>
              <div className="pr-col">
                <dl className="kv">
                  <dt>Fichier</dt><dd className="mono">{FILE.name}</dd>
                  <dt>Empreinte</dt><dd className="mono">{FILE.sha.slice(0, 12)}…</dd>
                  <dt>Taille</dt><dd className="mono">{FILE.sizeMB} Mo</dd>
                </dl>

                {s.status === 'DRAFT' && <div><Button variant="primary" onClick={() => setS((p) => to(p, 'PREPARED', { notice: null }, 'Projet préparé'))}>Préparer le fichier</Button></div>}
                {s.status === 'PREPARED' && <div className="row-wrap"><Button variant="primary" onClick={() => setS((p) => to(p, 'SLICING', { progress: 0, notice: null }))}>Lancer le slicing et la validation</Button><span className="src-note">Simulé, durée réelle inconnue.</span></div>}
                {s.status === 'SLICING' && <Progress label="Slicing et validation" value={s.progress} />}

                {['VALIDATED', 'UPLOADING', 'UPLOADED', 'AWAITING_PRINT_CONFIRMATION', 'START_REQUESTED', 'PRINTING', 'COMPLETED', 'FAILED', 'CANCELLED', 'START_UNKNOWN'].includes(s.status) && (
                  <Report items={report} />
                )}
                {s.status === 'VALIDATED' && (
                  <div className="row-wrap">
                    <Button variant="primary" disabled={!s.printerOk || blockers.length > 0} onClick={() => setS((p) => to(p, 'UPLOADING', { progress: 0, notice: null }, 'Envoi vers la P1S (simulé)'))}>Envoyer sur la P1S</Button>
                    <span className="src-note">L'envoi ne déclenche ni chauffage ni mouvement.</span>
                  </div>
                )}
                {s.status === 'UPLOADING' && <Progress label={`Transfert vers la P1S (simulé) — ${(FILE.sizeMB * s.progress / 100).toFixed(2)} / ${FILE.sizeMB} Mo`} value={s.progress} />}
                {s.status === 'UPLOADED' && (
                  <div className="row-wrap">
                    <Button variant="primary" disabled={!s.printerOk} onClick={() => setS((p) => to(p, 'AWAITING_PRINT_CONFIRMATION', { notice: null, emptyChecked: false }, 'Confirmation demandée à l\'utilisateur'))}>Demander la confirmation de lancement</Button>
                    <span className="src-note">Fichier présent sur la P1S, rien ne tourne.</span>
                  </div>
                )}
                {s.status === 'START_REQUESTED' && <div role="status" className="row-wrap"><span className="spinner" aria-hidden /> Commande de démarrage envoyée, en attente de l'accusé et de l'état PRINTING…</div>}
                {s.status === 'COMPLETED' && <div className="row-wrap"><Tag tone="ok">terminé</Tag> Pense à retirer la pièce avant la prochaine impression.</div>}
                {s.status === 'FAILED' && <div className="row-wrap"><Tag tone="err">échec</Tag> L'impression s'est arrêtée à la couche {s.layer}. Inspecte la pièce et le plateau avant de relancer.</div>}
                {s.status === 'CANCELLED' && <div className="row-wrap"><Tag tone="warn">annulé</Tag> Annulation demandée par toi à la couche {s.layer}.</div>}
                {['COMPLETED', 'FAILED', 'CANCELLED'].includes(s.status) && <div><Button variant="secondary" onClick={reset}>Nouveau brouillon</Button></div>}
              </div>
            </Panel>

            {['PRINTING', 'COMPLETED', 'FAILED', 'CANCELLED'].includes(s.status) && (
              <Panel title="Suivi d'impression" actions={<Tag>valeurs simulées (démo)</Tag>}>
                <div className="pr-col">
                  <div className="tele">
                    <div><span className="n">{s.layer} / {TOTAL_LAYERS}</span><span className="l">couche</span></div>
                    <div><span className="n">{fmt(elapsed)}</span><span className="l">écoulé (démo)</span></div>
                    <div><span className="n">{fmt(remaining)}</span><span className="l">restant (démo)</span></div>
                    <div><span className="n">{s.status === 'PRINTING' ? '220 °C' : '—'}</span><span className="l">buse (démo)</span></div>
                    <div><span className="n">{s.status === 'PRINTING' ? '60 °C' : '—'}</span><span className="l">plateau (démo)</span></div>
                  </div>
                  <Progress label="Avancement par couches" value={Math.round(s.layer / TOTAL_LAYERS * 100)} />
                  {s.status === 'PRINTING' && <div><Button variant="danger" onClick={() => setS((p) => to(p, 'CANCELLED', { confirmation: null, emptyChecked: false }, `Annulation à la couche ${p.layer}`))}>Annuler l'impression</Button></div>}
                </div>
              </Panel>
            )}
          </div>

          <div className="pr-col">
            <Panel title="Paramètres de lancement">
              <div className="pr-col">
                <Field label="Imprimante"><Select disabled value={demoPrinter.id} onChange={() => {}} options={[{ value: demoPrinter.id, label: demoPrinter.name }]} /></Field>
                <Field label="Plateau"><Select disabled={locked} value={s.plate} onChange={(e) => change({ plate: e.target.value }, 'le plateau')} options={plates} /></Field>
                <Field label="Affectation matière">
                  <Select disabled={locked} value={s.materialId} onChange={(e) => change({ materialId: e.target.value }, 'l\'affectation matière')}
                    options={[{ value: '', label: 'Non affectée' }, ...demoMaterials.map((m) => ({ value: m.id, label: `${m.family} — ${m.reference ?? 'référence inconnue'}` }))]} />
                </Field>
                <p className="src-note">{locked ? 'Verrouillé : une impression est en cours ou terminée.' : 'Modifier un de ces éléments invalide la confirmation déjà donnée.'}</p>
              </div>
            </Panel>

            <Panel title="Cas de démonstration" actions={<Tag>démo</Tag>}>
              <div className="pr-col">
                <Toggle label="Imprimante indisponible" checked={!s.printerOk} disabled={['START_REQUESTED', 'PRINTING', 'COMPLETED', 'FAILED', 'CANCELLED', 'START_UNKNOWN'].includes(s.status)}
                  onChange={(v) => setS((p) => ({ ...p, printerOk: !v, log: withLog(p, v ? 'Imprimante indisponible (démo)' : 'Imprimante de nouveau joignable') }))} />
                <Toggle label="Windows éteint (fichier seulement sur Windows)" checked={!s.fileOnLinux} disabled={locked || s.status === 'VALIDATED' || s.status === 'UPLOADED' || s.status === 'AWAITING_PRINT_CONFIRMATION'}
                  onChange={(v) => setS((p) => ({ ...p, fileOnLinux: !v, log: withLog(p, v ? 'Windows éteint (démo)' : 'Fichier copié sur Linux') }))} />
                <Toggle label="Perte réseau juste après le démarrage" checked={s.armNetLoss} disabled={['START_REQUESTED', 'PRINTING', 'COMPLETED', 'FAILED', 'CANCELLED', 'START_UNKNOWN'].includes(s.status)}
                  onChange={(v) => setS((p) => ({ ...p, armNetLoss: v }))} />
                <Toggle label="Échec en cours d'impression" checked={s.armFail} disabled={['COMPLETED', 'FAILED', 'CANCELLED'].includes(s.status)} onChange={(v) => setS((p) => ({ ...p, armFail: v }))} />
                <div><Button size="sm" variant="ghost" onClick={() => setS({ ...initial, log: [{ t: clock(), text: 'Prototype réinitialisé' }] })}>Tout réinitialiser</Button></div>
              </div>
            </Panel>

            <Panel title="Journal (simulé)">
              {s.log.length === 0 ? <p className="unknown">Aucun événement.</p> : <ul className="log">{s.log.map((l, i) => <li key={i}><span>{l.t}</span><span>{l.text}</span></li>)}</ul>}
            </Panel>
          </div>
        </div>
      </div>
    </>
  );
}

function Progress({ label, value }: { label: string; value: number }) {
  return (
    <div className="pr-col" style={{ gap: 'var(--sp-1)' }}>
      <span className="src-note">{label} — {value} %</span>
      <div className="bar" role="progressbar" aria-label={label} aria-valuemin={0} aria-valuemax={100} aria-valuenow={value}><span style={{ width: `${value}%` }} /></div>
    </div>
  );
}

function Report({ items }: { items: Check[] }): ReactNode {
  return (
    <div>
      <h3 style={{ fontSize: 'var(--fs-md)', marginBottom: 'var(--sp-2)' }}>Rapport de validation du fichier</h3>
      <ul className="report">
        {items.map((c) => (
          <li key={c.label}>
            <span className={`mk mk-${c.tone}`} aria-hidden />
            <span><strong>{c.tone === 'ok' ? 'OK' : c.tone === 'warn' ? 'Attention' : 'Bloquant'} — {c.label}</strong><br /><span className="why">{c.why}</span></span>
          </li>
        ))}
      </ul>
    </div>
  );
}
