import { useEffect } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { Plus } from 'lucide-react';
import { PageHeader, Skeleton, Tabs } from '../components';
import { api } from '../api/client';
import { useAsync } from '../api/hooks';
import { tabsOf } from '../nav';
import { useTab } from '../tabs';
import { JobsView, NewDirectJob, isTodoJob } from './Print';
import { QueueTab } from './Filament';
import { PickModel } from './Prepare';
import { CostsOverview } from './CostsOverview';
import { InsightsDashboard } from './InsightsDashboard';
import { InsightsJournal } from './InsightsJournal';
import './insights.css';

const newPrint = (
  <Link className="btn btn-primary" to="/impressions/nouvelle"><Plus size={16} aria-hidden />Nouvelle impression</Link>
);

/**
 * Impressions : tout ce qui est une impression, du dépôt au bilan. Quatre vues (?vue=) :
 * À faire (attend un geste de vous), En cours et en file, Terminées (journal), Bilan (chiffres et coûts).
 * Les écrans existants (historique des travaux, file d'attente, journal, vue d'ensemble des coûts) sont déplacés ici, logique inchangée.
 */
export function ImpressionsPage() {
  const [sp] = useSearchParams();
  const jobs = useAsync((s) => api.printJobs(s, 'all'), []);
  const todo = (jobs.data?.print_jobs ?? []).filter(isTodoJob).length;
  const [tab, setTab] = useTab('/impressions', jobs.data && todo > 0 ? 'a-faire' : 'en-cours');
  const waiting = !sp.get('vue') && !jobs.data && !jobs.error;

  // ancre « #usage » (anciens liens vers l'onglet Usage des coûts)
  useEffect(() => {
    if (tab === 'bilan' && window.location.hash === '#usage') document.getElementById('usage')?.scrollIntoView();
  }, [tab, jobs.data]);

  return (
    <>
      <PageHeader title="Impressions" help="Ce qui est à faire, en cours et terminé. Rien ne démarre sans votre accord sur la page de confirmation."
        actions={newPrint} below={<Tabs label="Vues des impressions" value={tab} onChange={setTab} tabs={tabsOf('/impressions').map((t) => (t.value === 'a-faire' && todo > 0 ? { ...t, label: `${t.label} (${todo})` } : t))} />} />
      {waiting ? <Skeleton lines={4} label="Chargement des impressions" /> : (
        <>
          {tab === 'a-faire' && (
            <JobsView title="À faire" views={['all']} filter={isTodoJob}
              empty={{ title: 'Rien n\'attend de vous', text: 'Les impressions préparées, envoyées à l\'imprimante ou prêtes à lancer apparaissent ici, avec le bouton « Vérifier et lancer ».', action: newPrint }} />
          )}
          {tab === 'en-cours' && (
            <div className="stack">
              <JobsView title="Préparations et impressions en cours" views={['active']} empty={{ title: 'Aucune impression en cours', text: 'Les préparations, envois et impressions en cours s\'affichent ici avec leur avancement.', action: newPrint }} />
              <QueueTab />
            </div>
          )}
          {tab === 'terminees' && (
            <div className="stack">
              <InsightsJournal />
              <JobsView title="Classer les impressions terminées" views={['finished', 'failed', 'archived', 'trash']} empty={{ title: 'Aucune impression dans cette vue', text: 'Les impressions terminées, en échec, archivées ou à la corbeille se classent ici.', action: newPrint }} />
            </div>
          )}
          {tab === 'bilan' && (
            <div className="stack">
              <CostsOverview />
              <section id="usage" aria-label="Usage détaillé"><InsightsDashboard /></section>
            </div>
          )}
        </>
      )}
    </>
  );
}

/** Impressions › Nouvelle impression : l'assistant commence par choisir un modèle ; l'objectif, les essais et la préparation suivent sur la fiche du modèle. */
export function NewPrintPage() {
  const nav = useNavigate();
  return (
    <>
      <PageHeader title="Nouvelle impression" help="Choisissez un modèle : l'assistant prépare l'impression ; le lancement se confirme ensuite sur la page du serveur." />
      <PickModel onPick={(m) => nav(`/modele/${encodeURIComponent(m)}/preparer`)} />
      <NewDirectJob />
    </>
  );
}
