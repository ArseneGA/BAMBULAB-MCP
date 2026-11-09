// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest';
import { LibraryCard, normalizeLibrary } from './library';
import { COLLECTION, MODEL, SEARCH } from './fixtures';
import { mockApp, mount, ok } from './testkit';

let m: ReturnType<typeof mount>;
afterEach(() => m?.unmount());

describe('carte Bibliothèque', () => {
  it('reconnaît search_models et list_collection, rien d\'autre', () => {
    expect(normalizeLibrary(SEARCH)).toMatchObject({ source: 'search', total: 7, hasMore: true });
    expect(normalizeLibrary(COLLECTION)).toMatchObject({ source: 'collection', collectionPath: 'Maison/Cuisine', subcollections: [{ id: 'col_2', path: 'Maison/Cuisine/Épices', total_models: 2 }] });
    expect(normalizeLibrary({ models: [{ id: 'x', name: 'y' }] })).toBeUndefined(); // liste de modèles d'imprimante : pas de total
    expect(normalizeLibrary({ printers: [] })).toBeUndefined();
    expect(normalizeLibrary(undefined)).toBeUndefined();
  });

  it('affiche la grille : nom, dimensions, étiquettes, format, Windows seul', async () => {
    m = mount();
    await m.render(<LibraryCard app={mockApp()} data={normalizeLibrary(SEARCH)!} onOpen={() => {}} />);
    const t = m.text();
    expect(t).toContain('Support de câbles'); expect(t).toContain('80 × 40 × 25 mm'); expect(t).toContain('Boîte à épices'); expect(t).toContain('3MF');
    expect(t).toContain('Windows seul'); expect(t).toContain('3 sur 7'); expect(t).toContain('d\'autres résultats existent');
    expect(m.host.querySelectorAll('.lc-tile')).toHaveLength(3);
    expect(m.host.querySelectorAll('svg.lc-box')).toHaveLength(3);
  });

  it('filtre par format', async () => {
    m = mount();
    await m.render(<LibraryCard app={mockApp()} data={normalizeLibrary(SEARCH)!} onOpen={() => {}} />);
    await m.click(/^STEP$/);
    expect(m.host.querySelectorAll('.lc-tile')).toHaveLength(2);
    expect(m.text()).not.toContain('Support de câbles');
    await m.click(/^STL$/);
    expect(m.host.querySelectorAll('.lc-tile')).toHaveLength(1);
    await m.click(/^Tous$/);
    expect(m.host.querySelectorAll('.lc-tile')).toHaveLength(3);
  });

  it('clic sur une pièce : get_model_details avec model_id, résultat transmis à la vue', async () => {
    const detail = ok({ model: { id: 'mod_2' }, files: [] });
    const app = mockApp({ get_model_details: () => detail });
    const opened: unknown[] = [];
    m = mount();
    await m.render(<LibraryCard app={app} data={normalizeLibrary(SEARCH)!} onOpen={(r) => opened.push(r)} />);
    await m.click(/Ouvrir la fiche de Boîte à épices/);
    expect(app.callServerTool).toHaveBeenCalledWith({ name: 'get_model_details', arguments: { model_id: 'mod_2' } });
    expect(opened).toEqual([detail]);
  });

  it('collection : sous-collection cliquable -> list_collection par chemin', async () => {
    const app = mockApp({ list_collection: () => ok(COLLECTION) });
    m = mount();
    await m.render(<LibraryCard app={app} data={normalizeLibrary(COLLECTION)!} onOpen={() => {}} />);
    expect(m.text()).toContain('Collection Maison/Cuisine');
    await m.click(/Épices/);
    expect(app.callServerTool).toHaveBeenCalledWith({ name: 'list_collection', arguments: { collection: 'Maison/Cuisine/Épices' } });
  });

  it('état vide et état erreur (outil en échec) propres', async () => {
    m = mount();
    await m.render(<LibraryCard app={mockApp()} data={normalizeLibrary({ models: [], total: 0, limit: 20, offset: 0, has_more: false })!} onOpen={() => {}} />);
    expect(m.text()).toContain('Aucun modèle ne correspond');
    m.unmount();
    const app = mockApp({ get_model_details: () => ({ isError: true, content: [{ type: 'text', text: 'modèle introuvable' }] }) });
    m = mount();
    await m.render(<LibraryCard app={app} data={normalizeLibrary({ models: [MODEL('mod_9', 'Vase')], total: 1, limit: 20, offset: 0, has_more: false })!} onOpen={() => { throw new Error('ne doit pas naviguer'); }} />);
    await m.click(/Ouvrir la fiche de Vase/);
    expect(m.host.querySelector('[role=alert]')?.textContent).toContain('modèle introuvable');
  });
});
