import { useSearchParams } from 'react-router-dom';
import { Tabs } from '../components';
import { CostsOverview } from './CostsOverview';
import { InsightsDashboard } from './InsightsDashboard';
import { InsightsJournal } from './InsightsJournal';
import { InsightsCosts } from './InsightsCosts';
import { InsightsPostProcessing } from './InsightsPostProcessing';
import './insights.css';

type Tab = 'overview' | 'dashboard' | 'journal' | 'costs' | 'post';
const TABS: Tab[] = ['overview', 'dashboard', 'journal', 'costs', 'post'];

/** Coûts : vue d'ensemble du mois, usage détaillé, journal des impressions, prix et estimation de coût, conseils de post-traitement. */
export function InsightsPage() {
  const [sp, setSp] = useSearchParams();
  const q = sp.get('onglet');
  const tab: Tab = TABS.includes(q as Tab) ? (q as Tab) : 'overview';
  const setTab = (t: Tab) => setSp(t === 'overview' ? {} : { onglet: t });
  return (
    <>
      <div className="page-head">
        <div>
          <h1>Coûts</h1>
          <div className="sub">Estimations à partir de vos tarifs : heures machine, réussite, matière et coût de vos impressions, journal de chaque impression et conseils de finition. Les hypothèses sont toujours indiquées.</div>
        </div>
      </div>
      <div className="stack">
        <Tabs<Tab> label="Sections" value={tab} onChange={setTab} tabs={[
          { value: 'overview', label: 'Vue d\'ensemble' }, { value: 'dashboard', label: 'Usage' }, { value: 'journal', label: 'Journal' }, { value: 'costs', label: 'Prix et coût' }, { value: 'post', label: 'Post-traitement' },
        ]} />
        {tab === 'overview' && <CostsOverview />}
        {tab === 'dashboard' && <InsightsDashboard />}
        {tab === 'journal' && <InsightsJournal />}
        {tab === 'costs' && <InsightsCosts />}
        {tab === 'post' && <InsightsPostProcessing />}
      </div>
    </>
  );
}
