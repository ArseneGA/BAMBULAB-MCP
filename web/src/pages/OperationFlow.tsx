import { useEffect, useState } from 'react';
import { ShieldCheck } from 'lucide-react';
import { Button, Tag } from '../components';
import { api, describeError } from '../api/client';
import type { ApplyResult, OpPreview, Operation } from '../api/types';
import { Dialog, Notice, fmtDate } from './common';
import './common.css';

type Phase =
  | { k: 'preview-loading' }
  | { k: 'preview'; pv: OpPreview }
  | { k: 'applying'; pv: OpPreview }
  | { k: 'done'; res: ApplyResult }
  | { k: 'error'; message: string; stale?: boolean };

/**
 * Aperçu PUIS application : rien n'est modifié tant que l'utilisateur n'a pas lu la portée
 * et les différences et confirmé (jeton à usage unique renvoyé par /api/operations/preview).
 */
export function OperationFlow({ title, operations, onClose, onApplied }: { title: string; operations: Operation[] | null; onClose: () => void; onApplied?: (r: ApplyResult) => void }) {
  const [phase, setPhase] = useState<Phase>({ k: 'preview-loading' });
  const [run, setRun] = useState(0);
  const open = operations !== null;

  useEffect(() => {
    if (!operations) return;
    let alive = true;
    setPhase({ k: 'preview-loading' });
    api.previewOps(operations).then((pv) => alive && setPhase({ k: 'preview', pv }), (e) => alive && setPhase({ k: 'error', message: describeError(e) }));
    return () => { alive = false; };
  }, [operations, run]);

  const apply = async (pv: OpPreview) => {
    setPhase({ k: 'applying', pv });
    try {
      const res = await api.applyOps(pv.token);
      setPhase({ k: 'done', res });
      onApplied?.(res);
    } catch (e) {
      const msg = describeError(e);
      // Jeton expiré, déjà utilisé ou état modifié depuis l'aperçu : il faut relancer l'aperçu.
      setPhase({ k: 'error', message: msg, stale: true });
    }
  };

  const foot = (() => {
    switch (phase.k) {
      case 'preview': return <>
        <Button onClick={onClose}>Annuler</Button>
        <Button variant={phase.pv.to_trash > 0 ? 'danger' : 'primary'} icon={<ShieldCheck size={15} />} onClick={() => apply(phase.pv)} disabled={phase.pv.changes.length === 0}>
          Appliquer {phase.pv.changes.length} changement{phase.pv.changes.length > 1 ? 's' : ''}
        </Button>
      </>;
      case 'applying': return <Button disabled>Application…</Button>;
      case 'done': return <Button variant="primary" onClick={onClose}>Fermer</Button>;
      case 'error': return <>
        <Button onClick={onClose}>Fermer</Button>
        {phase.stale && <Button onClick={() => setRun((r) => r + 1)}>Relancer l'aperçu</Button>}
      </>;
      default: return <Button onClick={onClose}>Annuler</Button>;
    }
  })();

  return (
    <Dialog open={open} onClose={onClose} title={title} wide footer={foot}>
      {phase.k === 'preview-loading' && <p role="status" aria-busy="true"><span className="spinner" aria-hidden /> Calcul de l'aperçu…</p>}
      {phase.k === 'error' && <Notice tone="err" title="L'opération n'a pas été appliquée">{phase.message}{phase.stale ? ' Rien n\'a été modifié : relancez l\'aperçu pour obtenir un nouveau jeton.' : ''}</Notice>}
      {(phase.k === 'preview' || phase.k === 'applying') && <PreviewBody pv={phase.pv} />}
      {phase.k === 'done' && (
        <Notice tone="info" icon={<ShieldCheck size={16} aria-hidden />} title={`${phase.res.applied} changement${phase.res.applied > 1 ? 's' : ''} appliqué${phase.res.applied > 1 ? 's' : ''}`}>
          {phase.res.note}
          {phase.res.trashed && phase.res.trashed.length > 0 && <> {phase.res.trashed.length} modèle(s) sont en corbeille et restent récupérables.</>}
        </Notice>
      )}
    </Dialog>
  );
}

function PreviewBody({ pv }: { pv: OpPreview }) {
  return (
    <>
      <p className="hint" style={{ margin: 0 }}>Aperçu uniquement : rien n'est encore modifié. Jeton valable jusqu'à <span className="mono">{fmtDate(pv.expires_at)}</span>.</p>
      <div className="kv" role="group" aria-label="Portée">
        <div><span className="k">Modèles</span><span className="v">{pv.models_affected}</span></div>
        <div><span className="k">Fichiers</span><span className="v">{pv.files_affected}</span></div>
        <div><span className="k">Corbeille</span><span className="v">{pv.to_trash}</span></div>
        <div><span className="k">Collections créées</span><span className="v">{pv.creates_collections?.length ?? 0}</span></div>
      </div>
      {pv.warnings && pv.warnings.length > 0 && <Notice title="À vérifier avant d'appliquer">{pv.warnings.join(' ')}</Notice>}
      {pv.creates_collections && pv.creates_collections.length > 0 && <p style={{ margin: 0 }}>Collections créées : {pv.creates_collections.map((c) => <Tag key={c} tone="accent">{c}</Tag>)}</p>}
      {pv.changes.length === 0 ? (
        <Notice tone="info" title="Aucun changement">Les opérations demandées ne modifieraient rien (valeurs déjà identiques).</Notice>
      ) : (
        <div style={{ overflow: 'auto', maxHeight: 320 }}>
          <table className="difftable">
            <thead><tr><th scope="col">Cible</th><th scope="col">Champ</th><th scope="col">Avant</th><th scope="col">Après</th></tr></thead>
            <tbody>
              {pv.changes.map((c, i) => (
                <tr key={i}>
                  <td>{c.target_name} <span className="sub mono" style={{ color: 'var(--text-3)' }}>{c.target_type === 'file' ? 'fichier' : 'modèle'}</span></td>
                  <td className="mono">{c.field}</td>
                  <td className="before">{c.before || '—'}</td>
                  <td className="after">{c.after || '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <p className="hint" style={{ margin: 0 }}>{pv.note}</p>
    </>
  );
}
