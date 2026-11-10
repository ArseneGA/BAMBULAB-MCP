import { useState } from 'react';
import { PageHeader, Select, Tabs } from '../components';
import { api } from '../api/client';
import { useAsync } from '../api/hooks';
import { tabsOf } from '../nav';
import { useTab } from '../tabs';
import { ColorsTab, SpoolsTab } from './Filament';
import { MaterialSheetsView } from './Materials';
import { InsightsPostProcessing } from './InsightsPostProcessing';
import './filament.css';
import './insights.css';

/**
 * Matières : tout ce qui est de la matière. Quatre vues (?vue=) : Bobines et AMS, Multicolore, Fiches matières (ex-Matériaux),
 * Après impression (ex-Coûts › Post-traitement). La file d'attente des impressions est dans Impressions › En cours et en file.
 */
export function MatieresPage() {
  const [tab, setTab] = useTab('/matieres');
  const [pid, setPid] = useState('');
  const printers = useAsync((s) => api.printers(s), []);
  const plist = printers.data?.printers ?? [];
  const printer = plist.find((p) => p.id === pid) ?? plist[0];
  return (
    <>
      <PageHeader title="Matières" help="Vos bobines et l'état de l'AMS, les couleurs, les fiches techniques des matières et les conseils de finition."
        below={(
          <div className="fil-bar">
            <Tabs label="Vues des matières" value={tab} onChange={setTab} tabs={tabsOf('/matieres')} />
            {(tab === 'bobines' || tab === 'multicolore') && plist.length > 1 && <Select aria-label="Imprimante" value={printer?.id ?? ''} onChange={(e) => setPid(e.target.value)} options={plist.map((p) => ({ value: p.id, label: p.name }))} />}
          </div>
        )} />
      {tab === 'bobines' && <SpoolsTab printerId={printer?.id} />}
      {tab === 'multicolore' && <ColorsTab printerId={printer?.id} />}
      {tab === 'fiches' && <MaterialSheetsView />}
      {tab === 'apres-impression' && <InsightsPostProcessing />}
    </>
  );
}
