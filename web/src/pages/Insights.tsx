import { useState } from 'react';
import { Tabs } from '../components';
import { InsightsDashboard } from './InsightsDashboard';
import { InsightsJournal } from './InsightsJournal';
import { InsightsCosts } from './InsightsCosts';
import { InsightsPostProcessing } from './InsightsPostProcessing';
import './insights.css';

type Tab = 'dashboard' | 'journal' | 'costs' | 'post';

/** Tableau de bord d'usage, journal des impressions, prix et estimation de coût, conseils de post-traitement. */
export function InsightsPage() {
  const [tab, setTab] = useState<Tab>('dashboard');
  return (
    <>
      <div className="page-head">
        <span className="eyebrow">Espace</span>
        <h1>Tableau de bord</h1>
        <p>Heures machine, réussite, matière et coût de vos impressions, le journal de chaque impression et les conseils de finition. Tous les coûts sont des estimations : leurs hypothèses sont indiquées.</p>
      </div>
      <div className="stack">
        <Tabs<Tab> label="Sections" value={tab} onChange={setTab} tabs={[
          { value: 'dashboard', label: 'Usage' }, { value: 'journal', label: 'Journal' }, { value: 'costs', label: 'Prix et coût' }, { value: 'post', label: 'Post-traitement' },
        ]} />
        {tab === 'dashboard' && <InsightsDashboard />}
        {tab === 'journal' && <InsightsJournal />}
        {tab === 'costs' && <InsightsCosts />}
        {tab === 'post' && <InsightsPostProcessing />}
      </div>
    </>
  );
}
