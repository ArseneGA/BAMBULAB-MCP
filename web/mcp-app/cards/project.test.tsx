// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest';
import { ProjectCard, normalizeProject } from './project';
import { classifyCard } from './index';
import { PROJECT_DETAIL, PROJECT_LIST, PROJECTS } from './fixtures';
import { mockApp, mount, ok } from './testkit';

let m: ReturnType<typeof mount>;
afterEach(() => m?.unmount());

describe('carte Projet', () => {
  it('reconnaît get_project et list_projects, sans voler la forme de get_project_costs', () => {
    expect(normalizeProject(PROJECT_DETAIL)?.kind).toBe('project');
    expect(normalizeProject(PROJECT_LIST)?.kind).toBe('list');
    expect(classifyCard(PROJECT_DETAIL)?.card).toBe('project');
    expect(classifyCard(PROJECT_LIST)?.card).toBe('project');
    expect(classifyCard({ projects: [], total: 0, applied_filters: {}, counts_by_status: {}, note: 'x' })?.card).toBe('project');
    expect(normalizeProject(PROJECTS)).toBeUndefined();
    expect(classifyCard(PROJECTS)?.card).toBe('costs');
    expect(normalizeProject({ project: { id: 'x', name: 'n', status: 'idea' }, progress: {}, parts: [], sub_projects: [] })).toBeUndefined();
  });

  it('fiche : statut en pastille avec texte, barre, coût, temps, pièces, sous-projets repliables, prochaine étape', async () => {
    m = mount();
    await m.render(<ProjectCard app={mockApp()} data={normalizeProject(PROJECT_DETAIL)!} onOpen={() => {}} />);
    const t = m.text();
    expect(t).toContain('Étagère'); expect(t).toContain('Idée'); expect(t).toContain('2/4 pièces imprimées'); expect(t).toContain('4/6 unités');
    expect(t).toContain('1,84'); expect(t).toContain('8 h'); expect(t).toContain('calculé'); expect(t).toContain('mesuré');
    expect(t).toContain('2/2 imprimées (1 jugée réussie, 1 jugée ratée)'); expect(t).toContain('Jugée ratée ou à refaire ×1');
    expect(t).toContain('Imprimée'); expect(t).toContain('À imprimer'); expect(t).toContain('collection adoptée');
    expect(t).toContain('1 tentative(s) en cours'); expect(t).toContain('Prochaine étape : Imprimer « Capot »');
    expect(m.host.querySelectorAll('details')).toHaveLength(2); // Pieds, puis Embouts imbriqué
    expect(m.host.querySelector('[role="meter"][aria-label="Avancement du projet Étagère"]')).not.toBeNull();
    expect(t).toContain('Statut suggéré : Impression'); expect(t).toContain('Rien ne change sans votre geste');
    // aucun geste de lancement, d'arrêt ou d'approbation
    expect(t).not.toMatch(/lancer|démarrer|approuver|arrêter|reprendre/i);
  });

  it('coût et temps « non disponibles » avec la raison, jamais un zéro inventé', async () => {
    m = mount();
    const d = { ...PROJECT_DETAIL, totals: { available: false, reason: 'aucune impression de ces pièces au journal', actual_cost: 0, actual_seconds: 0, actual_grams: 0, in_progress: 0, attempts: 0 }, suggested_status: undefined };
    await m.render(<ProjectCard app={mockApp()} data={normalizeProject(d)!} onOpen={() => {}} />);
    expect(m.text()).toContain('non disponible'); expect(m.text()).toContain('aucune impression de ces pièces au journal');
    expect(m.text()).not.toContain('0,00');
  });

  it('gestes : statut et quantité passent par les outils réservés à la carte', async () => {
    m = mount();
    const calls: Array<[string, Record<string, unknown>]> = [];
    const app = mockApp({
      submit_project_status: (a) => { calls.push(['submit_project_status', a]); return ok(PROJECT_DETAIL); },
      submit_project_part: (a) => { calls.push(['submit_project_part', a]); return ok(PROJECT_DETAIL); },
      get_part_sheet: (a) => { calls.push(['get_part_sheet', a]); return ok({}); },
    });
    await m.render(<ProjectCard app={app} data={normalizeProject(PROJECT_DETAIL)!} onOpen={() => {}} />);
    await m.click(/^Conception$/);
    await m.click(/Passer en « Impression »/);
    await m.click(/\+ quantité/);
    await m.click(/Ouvrir la pièce Équerre/);
    expect(calls).toEqual([
      ['submit_project_status', { project_id: 'prj_aaaa', status: 'design' }],
      ['submit_project_status', { project_id: 'prj_aaaa', status: 'printing' }],
      ['submit_project_part', { project_id: 'prj_aaaa', model_id: 'mod_1', quantity: 3 }],
      ['get_part_sheet', { model_id: 'mod_1' }],
    ]);
  });

  it('erreur d\'un outil affichée, pas de plantage', async () => {
    m = mount();
    const app = mockApp({ submit_project_status: () => ({ isError: true, content: [{ type: 'text', text: 'statut refusé' }] }) });
    await m.render(<ProjectCard app={app} data={normalizeProject(PROJECT_DETAIL)!} onOpen={() => {}} />);
    await m.click(/^Terminé$/);
    expect(m.text()).toContain('statut refusé');
  });

  it('liste : arbre avec indentation, statut, avancement ; clic = fiche ; état vide explicite', async () => {
    m = mount();
    const calls: string[] = [];
    const app = mockApp({ get_project: (a) => { calls.push(String(a.id)); return ok(PROJECT_DETAIL); } });
    await m.render(<ProjectCard app={app} data={normalizeProject(PROJECT_LIST)!} onOpen={() => {}} />);
    expect(m.text()).toContain('Étagère'); expect(m.text()).toContain('Pieds'); expect(m.text()).toContain('Archivé'); expect(m.text()).toContain('2/4 pièces imprimées');
    await m.click(/Projet Pieds/);
    expect(calls).toEqual(['prj_bbbb']);
    m.unmount();
    m = mount();
    await m.render(<ProjectCard app={mockApp()} data={normalizeProject({ projects: [], total: 0, applied_filters: {}, counts_by_status: {}, note: 'Aucun projet' })!} onOpen={() => {}} />);
    expect(m.text()).toContain('Aucun projet'); expect(m.text()).toContain('create_project');
  });
});
