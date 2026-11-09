package humanauth

import "strings"

// Habillage commun des pages HUMAINES rendues côté serveur (OAuth, /confirm, /stop, /action, /enroll, /setup/*, pages
// publiques, erreurs). Même direction artistique que l'interface web (web/src/design/tokens.css) : vert Printalk,
// verre, rayons arrondis, boutons de 34 px, clair et sombre (prefers-color-scheme).
//
// Contraintes de sécurité : la feuille de style est EMBARQUÉE dans chaque page (<style>, déjà permis par
// `style-src 'unsafe-inline'`), le logo est un SVG en ligne, aucune police ni image externe, aucun script. Les CSP
// des pages (web.go) ne sont donc pas élargies.

// ProductName et Tagline : SEULE source côté Go du nom et de la baseline (équivalent de web/src/brand.ts).
const (
	ProductName = "Printalk"
	Tagline     = "Fabriquer en parlant"
)

// PageMarker : attribut posé sur <body> par le gabarit commun (les tests vérifient que chaque page l'utilise).
const PageMarker = `data-printalk="page"`

// logoSVG : docs/logo/atelier-icon.svg, en ligne (identifiants de dégradés préfixés pour éviter toute collision).
const logoSVG = `<svg class="pt-logo" viewBox="0 0 512 512" aria-hidden="true" focusable="false"><defs>` +
	`<linearGradient id="pt-bg" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#3ee39c"/><stop offset="0.5" stop-color="#14a26c"/><stop offset="1" stop-color="#095a40"/></linearGradient>` +
	`<radialGradient id="pt-glow" cx=".5" cy=".5" r=".5"><stop offset="0" stop-color="#eafff5"/><stop offset=".45" stop-color="#9dffd2" stop-opacity=".9"/><stop offset="1" stop-color="#9dffd2" stop-opacity="0"/></radialGradient>` +
	`<linearGradient id="pt-shine" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#fff" stop-opacity=".25"/><stop offset=".45" stop-color="#fff" stop-opacity="0"/></linearGradient></defs>` +
	`<rect x="16" y="16" width="480" height="480" rx="120" fill="url(#pt-bg)"/><rect x="16" y="16" width="480" height="480" rx="120" fill="url(#pt-shine)"/>` +
	`<g fill="#fff"><rect x="112" y="300" width="288" height="44" rx="22"/><rect x="140" y="360" width="232" height="44" rx="22"/><path d="M150 396 h64 l-70 62 c-8 7 -19 0 -16 -10 z"/><rect x="140" y="240" width="168" height="44" rx="22"/></g>` +
	`<g fill="#fff"><rect x="262" y="96" width="112" height="56" rx="16"/><path d="M286 152 h64 l-22 50 h-20 z"/></g>` +
	`<circle cx="318" cy="226" r="34" fill="url(#pt-glow)"/><circle cx="318" cy="222" r="10" fill="#fff"/></svg>`

// Style est la feuille de style commune des pages humaines (jetons de web/src/design/tokens.css).
const Style = `:root{color-scheme:light dark;--p-strong:#16a06b;--p-deep:#095a40;--bg:#f4f7f5;--text:#14201b;--text2:#4a5750;--text3:#5b6862;--accent-text:#0b6a46;
--primary:#0f7a52;--primary-hover:#0c6844;--primary-text:#fff;--solid:#fff;--card:rgba(255,255,255,.88);--hair:rgba(9,90,64,.18);--well:rgba(9,90,64,.06);--line:#8fa69a;
--ok-text:#18714a;--ok-soft:#d9efe3;--ok-line:#8cc9a8;--warn-text:#8a5700;--warn-soft:#f6e8c8;--warn-line:#d7b566;--err-text:#a82a2f;--err-soft:#f7dcdc;--err-soft-hover:#f2c8c8;--err-line:#e3a4a6;
--shadow:0 1px 2px rgba(9,90,64,.05),0 14px 36px -14px rgba(9,90,64,.24);--shadow-primary:inset 0 1px 0 rgba(255,255,255,.22),0 8px 18px -10px rgba(22,160,107,.65);
--sans:'DM Sans',system-ui,-apple-system,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;--mono:'JetBrains Mono',ui-monospace,'Cascadia Mono',Consolas,monospace}
@media (prefers-color-scheme:dark){:root{--p-strong:#3ee39c;--p-deep:#0b2a20;--bg:#0f1714;--text:#eaf2ee;--text2:#b2beb8;--text3:#9aa8a1;--accent-text:#5fe8b0;
--primary:#3ee39c;--primary-hover:#6dedb4;--primary-text:#052018;--solid:#16211c;--card:rgba(22,33,28,.88);--hair:rgba(255,255,255,.14);--well:rgba(255,255,255,.06);--line:#4f665b;
--ok-text:#6fe09a;--ok-soft:#14351f;--ok-line:#2f7a52;--warn-text:#f7bb55;--warn-soft:#3a2f12;--warn-line:#7a5f1c;--err-text:#f08084;--err-soft:#40201d;--err-soft-hover:#4d2825;--err-line:#8a3a3d;
--shadow:0 1px 2px rgba(0,0,0,.3),0 14px 36px -14px rgba(0,0,0,.65);--shadow-primary:inset 0 1px 0 rgba(255,255,255,.3),0 8px 18px -10px rgba(62,227,156,.55)}}
*,*::before,*::after{box-sizing:border-box}
html{-webkit-text-size-adjust:100%}
[hidden]{display:none!important}
body{margin:0;min-height:100vh;background:var(--bg);color:var(--text);font:400 15px/1.55 var(--sans);-webkit-font-smoothing:antialiased}
body[data-printalk]{background-image:radial-gradient(46rem 22rem at 10% -6%,color-mix(in srgb,var(--p-strong) 18%,transparent),transparent 70%),radial-gradient(38rem 20rem at 100% 0,color-mix(in srgb,var(--p-strong) 10%,transparent),transparent 70%);background-repeat:no-repeat}
.pt-wrap{max-width:700px;margin:0 auto;padding:22px 16px 34px}
.pt-head{display:flex;align-items:center;gap:12px;margin:2px 4px 18px}
.pt-logo{width:42px;height:42px;flex:none;filter:drop-shadow(0 6px 10px rgba(9,90,64,.28))}
.pt-name{display:block;font-weight:700;font-size:1.15rem;line-height:1.2;letter-spacing:-.01em}
.pt-tag{display:block;font-size:.8125rem;color:var(--text2)}
main.pt-card{background:var(--solid);background:var(--card);border:1px solid var(--hair);border-radius:22px;padding:26px 28px;box-shadow:var(--shadow)}
@supports (backdrop-filter:blur(1px)) or (-webkit-backdrop-filter:blur(1px)){main.pt-card{-webkit-backdrop-filter:blur(26px) saturate(185%);backdrop-filter:blur(26px) saturate(185%)}}
.pt-foot{margin:18px 4px 0;text-align:center;color:var(--text3);font-size:.78rem;line-height:1.5}
h1{font-size:clamp(1.4rem,1.1rem + 1.2vw,1.7rem);line-height:1.2;font-weight:700;letter-spacing:-.02em;margin:0 0 1rem}
h2{font-size:1.125rem;line-height:1.25;font-weight:600;letter-spacing:-.015em;margin:1.7rem 0 .6rem}
h2 small{font-weight:400}
p{margin:.75rem 0}
ul{margin:.6rem 0;padding-left:1.3rem}
a{color:var(--accent-text);text-underline-offset:2px}
small,.dim,.meta{color:var(--text2)}
small{font-size:.85em}
strong{font-weight:700}
hr{border:0;border-top:1px solid var(--hair);margin:1.6rem 0}
code{font:.84rem var(--mono);background:var(--well);border-radius:6px;padding:.1em .38em;overflow-wrap:anywhere;word-break:break-word}
dl{display:grid;grid-template-columns:minmax(7.5rem,14rem) 1fr;gap:.6rem 1.1rem;background:var(--well);border:1px solid var(--hair);border-radius:14px;padding:.95rem 1.05rem;margin:1.1rem 0}
dt{color:var(--text2);font-size:.875rem}
dd{margin:0;overflow-wrap:anywhere;min-width:0}
.warn,.ok{border:1px solid;border-left-width:5px;border-radius:12px;padding:.7rem .95rem;margin:.95rem 0;color:var(--text)}
p.warn,div.warn{background:var(--warn-soft);border-color:var(--warn-line);border-left-color:var(--warn-text)}
p.ok,div.ok{background:var(--ok-soft);border-color:var(--ok-line);border-left-color:var(--ok-text)}
p.warn.bad,p.bad.warn{background:var(--err-soft);border-color:var(--err-line);border-left-color:var(--err-text)}
span.ok{color:var(--ok-text);font-weight:600;border:0;padding:0;margin:0}
span.warn{color:var(--warn-text);font-weight:600;border:0;padding:0;margin:0}
.bad{color:var(--err-text);font-weight:600}
p.warn.bad,p.bad.warn{color:var(--text);font-weight:400}
p.warn.bad strong{color:var(--err-text)}
label{font-weight:500}
input[type=text],input[type=password],input:not([type]),select,textarea{width:100%;min-height:38px;margin-top:.3rem;padding:.45rem .75rem;font:inherit;color:var(--text);background:var(--solid);border:1px solid var(--line);border-radius:10px}
input[size]{width:auto;max-width:100%}
input[type=checkbox]{flex:none;width:1.25rem;height:1.25rem;margin:.15rem 0 0;accent-color:var(--primary)}
label.chk{display:flex;gap:.75rem;align-items:flex-start;margin:1rem 0;padding:.85rem 1rem;font-weight:600;background:var(--well);border:1px solid var(--line);border-radius:12px;cursor:pointer}
label.chk:has(input:checked){border-color:var(--ok-text);background:var(--ok-soft)}
button{appearance:none;-webkit-appearance:none;display:inline-flex;align-items:center;justify-content:center;gap:.5rem;min-height:34px;padding:.4rem 1.15rem;border:1px solid transparent;border-radius:10px;font:600 .9375rem/1.2 var(--sans);cursor:pointer;background:var(--primary);color:var(--primary-text);box-shadow:var(--shadow-primary);transition:background-color .16s,transform .09s}
button:hover{background:var(--primary-hover)}
button:active{transform:translateY(1px)}
button:disabled{opacity:.55;cursor:not-allowed}
button.danger{background:var(--err-soft);color:var(--err-text);border-color:var(--err-line);box-shadow:none}
button.danger:hover{background:var(--err-soft-hover)}
button.sec{background:transparent;color:var(--text);border-color:var(--line);box-shadow:none}
button.sec:hover{background:var(--well)}
button+button{margin-left:.5rem}
a>button{pointer-events:none}
:focus-visible{outline:3px solid var(--primary);outline-offset:2px}
input:focus-visible,select:focus-visible,textarea:focus-visible{outline-offset:0}
figure{margin:1.1rem 0}
figure img,img.plate-photo{display:block;max-width:100%;height:auto;border:1px solid var(--line);border-radius:14px}
figcaption{margin-top:.55rem;font-size:.875rem;color:var(--text2)}
.tw{overflow-x:auto;margin:1rem 0}
table{border-collapse:collapse;width:100%}
th,td{border-bottom:1px solid var(--hair);padding:.5rem .6rem;text-align:left;vertical-align:top}
th{font-size:.8125rem;font-weight:600;color:var(--text2)}
.revoked{color:var(--text2);text-decoration:line-through}
@media (max-width:540px){
.pt-wrap{padding:14px 12px 26px}
main.pt-card{padding:18px 16px;border-radius:18px}
dl{grid-template-columns:1fr;gap:.1rem}
dt{margin-top:.65rem}dt:first-child{margin-top:0}
form>button,form>p>button{width:100%}
form>button,form>p>button+button,button+button{margin:.5rem 0 0}
}
@media (pointer:coarse){button{min-height:44px}input[type=text],input[type=password],input:not([type]),select{min-height:44px}}
@media (prefers-reduced-motion:reduce){*,*::before,*::after{transition-duration:.001ms!important;animation-duration:.001ms!important}}
@media (forced-colors:active){button{border-color:ButtonText}}`

// Layout renvoie les définitions de gabarit "head" et "foot" du gabarit commun (à placer en tête du texte d'un
// html/template). title : titre fixe de la page ; vide, c'est {{.Title}} des données. headExtra : balises ajoutées
// dans <head> (méta, lien d'icône, rafraîchissement). mainStart : contenu placé juste après l'ouverture de la carte
// (par exemple <h1>{{.Title}}</h1>). Le titre d'onglet est « titre · Printalk ».
func Layout(title, headExtra, mainStart string) string {
	t := "{{.Title}}"
	if title != "" {
		t = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "{", "&#123;").Replace(title)
	}
	return `{{define "head"}}<!doctype html><html lang="fr"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer">` + headExtra + `<title>` + t + ` · ` + ProductName + `</title><style>` + Style + `</style></head>
<body ` + PageMarker + `><div class="pt-wrap"><header class="pt-head">` + logoSVG + `<div><span class="pt-name">` + ProductName + `</span><span class="pt-tag">` + Tagline + `</span></div></header>
<main class="pt-card">` + mainStart + `{{end}}
{{define "foot"}}</main><footer class="pt-foot">` + ProductName + ` · ` + Tagline + `<br>Compatible avec les imprimantes Bambu Lab</footer></div></body></html>{{end}}
`
}
