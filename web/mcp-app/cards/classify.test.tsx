// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest';
import { CardView, classifyCard } from './index';
import { AMS, COLLECTION, COMPARE, DASHBOARD, ESTIMATE, HISTORY, PRINTERS, PRINT_LOG, PROJECTS, QUEUE, SEARCH, SPOOLS } from './fixtures';
import { mockApp, mount } from './testkit';

let m: ReturnType<typeof mount>;
afterEach(() => m?.unmount());

describe('classifyCard : une forme de résultat = une carte', () => {
  const cases: Array<[string, unknown, string]> = [
    ['search_models', SEARCH, 'library'], ['list_collection', COLLECTION, 'library'], ['compare_variants', COMPARE, 'compare'],
    ['estimate_cost', ESTIMATE, 'costs'], ['get_project_costs', PROJECTS, 'costs'], ['get_usage_dashboard', DASHBOARD, 'costs'],
    ['list_spools', SPOOLS, 'spools'], ['get_ams_state', AMS, 'spools'], ['list_print_history', HISTORY, 'prints'], ['list_print_queue', QUEUE, 'prints'],
    ['get_print_log', PRINT_LOG, 'prints'], ['list_printers', PRINTERS, 'printers'],
  ];
  it.each(cases)('%s -> %s', (_t, data, card) => { expect(classifyCard(data)?.card).toBe(card); });

  it('laisse passer les formes des autres vues (suivi, progression, fiche modèle, confirmation)', () => {
    for (const s of [{ job: { id: 'pj_1', state: 'PRINTING' }, printer: { state: 'printing' } }, { job_id: 'j', state: 'running', percent: 3 }, { model: { id: 'm' }, files: [], versions: [] }, { confirmation_id: 'c', binding: {} }, { sheet_layers: [] }, 'texte', null, undefined, 3]) {
      expect(classifyCard(s)).toBeUndefined();
    }
  });

  it('chaque carte se rend sans lever d\'erreur depuis CardView', async () => {
    for (const [, data] of cases) {
      m = mount();
      await m.render(<CardView app={mockApp({ get_printer_status: () => ({ structuredContent: {} }) })} card={classifyCard(data)!} onOpen={() => {}} />);
      expect(m.host.querySelector('section.lc')).not.toBeNull();
      m.unmount();
    }
  });
});
