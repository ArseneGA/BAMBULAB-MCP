// Contrôle automatique : débordements, textes tronqués, erreurs console, canvas 3D, contraste.
// Usage : node scripts/audit.mjs [baseUrl]   (serveur de dev déjà lancé sur 5173)
import { chromium } from 'playwright';
const base = process.argv[2] ?? 'http://localhost:5173';
const routes = ['/', '/bibliotheque', '/modele/m01', '/preparation', '/comparaison', '/materiaux', '/impression', '/claude', '/design'];
const browser = await chromium.launch({ args: ['--use-gl=angle', '--use-angle=swiftshader', '--enable-unsafe-swiftshader', '--ignore-gpu-blocklist'] });
let problems = 0;
for (const theme of ['light', 'dark']) for (const w of [800, 1440]) {
  const ctx = await browser.newContext({ viewport: { width: w, height: 900 } });
  await ctx.addInitScript((t) => localStorage.setItem('atelier-theme', t), theme);
  const page = await ctx.newPage();
  const errs = [];
  page.on('console', (m) => { if (m.type() === 'error') errs.push(m.text()); });
  page.on('pageerror', (e) => errs.push('pageerror ' + e.message));
  for (const r of routes) {
    errs.length = 0;
    await page.goto(base + r, { waitUntil: 'load' });
    await page.waitForTimeout(1500);
    const res = await page.evaluate(() => {
      const out = { hscroll: document.documentElement.scrollWidth - innerWidth, trunc: [], over: [], lowc: [], canvases: [], blank: document.getElementById('root').innerText.trim().length < 20 };
      const lum = (c) => { const [r, g, b] = c.map((v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; }); return 0.2126 * r + 0.7152 * g + 0.0722 * b; };
      const parse = (s) => { const m = s.match(/rgba?\(([^)]+)\)/); if (!m) return null; const p = m[1].split(/[ ,\/]+/).filter(Boolean).map(Number); return { c: p.slice(0, 3), a: p[3] ?? 1 }; };
      const bgOf = (el) => { for (let e = el; e; e = e.parentElement) { const p = parse(getComputedStyle(e).backgroundColor); if (p && p.a > 0.9) return p.c; } return [255, 255, 255]; };
      const sel = (e) => (e.tagName.toLowerCase() + (e.className && typeof e.className === 'string' ? '.' + e.className.trim().split(/\s+/).join('.') : '')).slice(0, 60);
      for (const e of document.querySelectorAll('body *')) {
        const cs = getComputedStyle(e);
        if (cs.display === 'none' || cs.visibility === 'hidden') continue;
        const rc = e.getBoundingClientRect(); if (!rc.width || !rc.height) continue;
        if ((cs.textOverflow === 'ellipsis' || cs.overflow !== 'visible') && e.scrollWidth > e.clientWidth + 1 && e.clientWidth > 0 && !e.closest('.cp-scroll,.sr-only') && !e.matches('.nav-label,.v3,.v3 *')) out.trunc.push(sel(e) + ' "' + (e.textContent || '').trim().slice(0, 30) + '"');
        if (rc.right > innerWidth + 1 && !e.closest('.cp-scroll') && !e.matches('.tip-bubble')) out.over.push(sel(e));
        const own = [...e.childNodes].some((n) => n.nodeType === 3 && n.textContent.trim());
        if (own && !e.closest('.sr-only')) {
          const fg = parse(cs.color); if (!fg) continue; const bg = bgOf(e);
          const L1 = lum(fg.c), L2 = lum(bg); const ratio = (Math.max(L1, L2) + 0.05) / (Math.min(L1, L2) + 0.05);
          const size = parseFloat(cs.fontSize); const need = size >= 18 || (size >= 14 && +cs.fontWeight >= 700) ? 3 : 4.5;
          if (ratio < need && fg.a > 0.5) out.lowc.push(`${ratio.toFixed(2)} ${sel(e)} "${(e.textContent || '').trim().slice(0, 24)}"`);
        }
      }
      for (const c of document.querySelectorAll('canvas')) {
        const gl = c.getContext('webgl2') || c.getContext('webgl');
        out.canvases.push(`${c.width}x${c.height}${gl ? '' : ' (ctx indisponible)'}`);
      }
      return out;
    });
    const issues = [];
    if (res.hscroll > 0) issues.push('scroll horizontal +' + res.hscroll);
    if (res.blank) issues.push('PAGE BLANCHE');
    if (res.trunc.length) issues.push('tronqué: ' + [...new Set(res.trunc)].slice(0, 6).join(' | '));
    if (res.over.length) issues.push('hors écran: ' + [...new Set(res.over)].slice(0, 6).join(' | '));
    if (res.lowc.length) issues.push('contraste: ' + [...new Set(res.lowc)].slice(0, 8).join(' | '));
    if (errs.length) issues.push('console: ' + errs.slice(0, 3).join(' | '));
    console.log(`${theme} ${w} ${r} canvases=[${res.canvases.join(',')}] ${issues.length ? '\n   ' + issues.join('\n   ') : 'OK'}`);
    problems += issues.length;
  }
  await ctx.close();
}
await browser.close();
console.log('Problèmes signalés :', problems);
