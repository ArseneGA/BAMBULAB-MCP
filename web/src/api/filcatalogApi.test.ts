import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from './client';
import { colorName, filcatalogApi, swatchBackground } from './filcatalogApi';
import type { CatalogColor } from './filcatalogApi';

const col = (hex: string[], name: Record<string, string>): CatalogColor => ({ id: 'GFA00-10300', family_id: 'GFA00', color_code: '10300', name, hex, kind: hex.length > 1 ? 'gradient' : 'single', confidence: 'official_profile' });

describe('catalogue officiel des filaments Bambu', () => {
  afterEach(() => vi.unstubAllGlobals());
  const stub = (status = 200, body: unknown = {}) => {
    const f = vi.fn(async () => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }));
    vi.stubGlobal('fetch', f);
    return f;
  };

  it('cherche les couleurs proches avec les paramètres attendus, sans paramètre vide', async () => {
    const f = stub();
    await filcatalogApi.colors({ family: 'PLA', hex: '#FF6A13', finish: '', hardened: false, limit: 48 });
    const [url, init] = f.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe('/api/filament-catalog/colors?family=PLA&hex=%23FF6A13&limit=48');
    expect(init.method).toBe('GET');
    expect(init.credentials).toBe('same-origin');
  });

  it('lit une fiche et remonte l\'erreur de l\'API', async () => {
    stub(404, { error: 'entrée inconnue', code: 'not_found' });
    await expect(filcatalogApi.entry('GFZ99')).rejects.toMatchObject({ status: 404, code: 'not_found' });
    stub(200, { family: { id: 'GFA00' } });
    expect((await filcatalogApi.entry('GFA00')).family.id).toBe('GFA00');
  });

  it('signale un serveur injoignable', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('fail'); }));
    await expect(filcatalogApi.overview()).rejects.toBeInstanceOf(ApiError);
  });

  it('nomme en français puis en anglais et trace un dégradé', () => {
    expect(colorName(col(['#FF6A13'], { fr: 'Orange', en: 'Orange X' }))).toBe('Orange');
    expect(colorName(col(['#FF6A13'], { en: 'Pumpkin' }))).toBe('Pumpkin');
    expect(swatchBackground(col(['#9CDBD9', '#FFFFFF'], { en: 'Arctic' }))).toBe('linear-gradient(135deg, #9CDBD9, #FFFFFF)');
    expect(swatchBackground(col(['#FF6A13'], { en: 'x' }))).toBe('#FF6A13');
  });
});
