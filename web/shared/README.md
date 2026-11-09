# Socle de design Printalk (`web/shared`)

Une seule source pour l'interface web (`web/src`) ET les cartes affichées dans Claude (`web/mcp-app`). Rien n'est dupliqué : `web/src/components/index.ts` ré-exporte ce socle, `web/src/api/progress.ts` ré-exporte `progress.ts`.

Alias : `@shared` (Vite des deux builds, vitest, `tsconfig.json`).

## Contenu

| Fichier | Rôle |
|---|---|
| `tokens.css` | jetons vert Printalk clair / sombre (`[data-theme='dark']`), espacements, rayons, `--control-h: 34px`, contrastes AA documentés en tête de fichier |
| `fonts.css` + `fonts/dm-sans-latin-wght.woff2` | DM Sans variable (latin, 37 Kio, licence OFL) **embarquée** : la CSP des cartes interdit les polices distantes ; Vite l'incorpore en `data:` dans le bundle monofichier. Repli : `var(--sans)` enchaîne sur la police système |
| `base.css` | reset, titres, focus visible, `.sr-only` |
| `theme.ts` | `useTheme()` du site (mémorisé), jetons exposés à la page /design |
| `host.ts` | thème des cartes : `applyHostContext(ctx)` (thème de l'hôte, sinon luminance de `--color-background-primary`), `followSystemTheme()` (repli `prefers-color-scheme`). Les variables de l'hôte sont recopiées sous `--host-*` et n'écrasent jamais la charte |
| `icons.ts` | icônes au trait (lucide) : seules celles listées sont embarquées |
| `progress.ts` | normalisation de l'avancement d'un travail |
| `components/ui.tsx` | `Button` (34 px), `IconButton`, `Field`, `Input`, `SearchInput`, `Select`, `Segmented`, `Toggle`, `Slider`, `Tag`, `StatusDot`, `Tooltip`, `Panel`, `Tabs`, `List`, `Tree`, `ToastProvider`/`useToast`, `EmptyState`, `LoadingState`, `ErrorState` |
| `components/kit.tsx` | `Card`, `Badge` (couleur + forme), `NumberField` (unité, virgule décimale), `TextField`, `SelectField`, `ColorField`, `CheckGroup`, `Table`, `Notice` |
| `components/ProgressBar.tsx` | `ProgressBar` (pourcentage, phase, reste, indéterminée sans valeur) |

## Utiliser le socle

```tsx
// point d'entrée (une fois)
import '@shared/tokens.css'; import '@shared/fonts.css'; import '@shared/base.css';
// composants et icônes
import { Card, Badge, Button, NumberField } from '@shared/components';
import { Printer } from '@shared/icons';

<Card title="Imprimante" icon={<Printer size={16} />} meta={<Badge tone="ok">En ligne</Badge>} footer={<Button variant="primary">Ouvrir</Button>}>…</Card>
```

Carte MCP App : thème de l'hôte.

```tsx
const stop = followSystemTheme(() => hostDriven);           // repli navigateur
app.onhostcontextchanged = (ctx) => { if (applyHostContext(ctx)) hostDriven = true; };
```

Règles : couleurs et tailles uniquement par jetons (`var(--primary)`…) ; un état n'est jamais exprimé par la couleur seule (`Badge` ajoute un point, `StatusDot` une forme) ; `body` des cartes est transparent (le fond est celui de la conversation), la surface est celle de `Card`.

## Cartes-formulaires

Voir `docs/research/mcp-apps.md` § 12 : `web/mcp-app/form.tsx` (rendu générique piloté par la spécification du serveur), `formLogic.ts` (validation, arguments). Un nouveau formulaire n'exige AUCUN code front : le serveur (`internal/mcpserver/forms.go`) décrit les champs, la carte les rend.
