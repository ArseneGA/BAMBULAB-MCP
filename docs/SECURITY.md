# Sécurité — modèle de menace et mécanismes

Date : 2026-11-06. Complète `docs/DOSSIER.md` §1 et §10 (« aucun lancement sans accord humain explicite, lié, par impression ; un booléen fourni par l'agent ne suffit pas »). Mécanismes détaillés : J7b (chaîne d'impression), J8 (agent Windows).

## 1. Modèle de menace

| Acteur | Capacités | Traitement |
|---|---|---|
| **Le modèle (Claude)** | n'a que les **outils MCP** (et leurs sorties) | **Doit JAMAIS pouvoir produire seul l'accord de lancement ni d'arrêt** d'une impression, ni lire un secret (code d'accès LAN, jeton de confirmation, code d'enregistrement, jeton d'appairage). C'est la menace de référence. |
| Un site web / une page ouverte dans le navigateur de l'humain | requêtes cross-origin | refusées (garde Host, `http.CrossOriginProtection`, cookie `SameSite=Strict`, `Sec-Fetch-*`, nonce de formulaire) |
| Un appareil du LAN | requêtes réseau vers bambud (si l'écoute n'est pas en loopback) | l'API JSON et `/mcp` exigent le Bearer (`--api-token`, obligatoire hors loopback) ; les pages humaines exigent le cookie d'un navigateur enregistré |
| Un agent Windows appairé | protocole `/agent/v1` authentifié par secret | ne peut ni lancer ni arrêter (les travaux physiques lui sont refusés) ; formats et tailles contrôlés |
| **Un processus local qui a les droits de l'utilisateur (shell)** | lit la base, le coffre, peut lancer `bambud enroll` | **Hors périmètre** : il pourrait aussi bien lire le coffre des codes d'accès ou piloter l'imprimante directement. On rend l'abus **difficile** (code à usage unique, cookie haché, révocation) et **visible** (journal d'audit : création de code, session, révocation, épinglage de certificat, arrêt). |

Conséquence de conception : **l'accord humain ne passe que par des pages HTML servies par bambud, derrière le cookie d'un navigateur enregistré**. Aucun outil MCP et aucune route JSON n'approuve un lancement ou un arrêt.

## 2. Navigateurs enregistrés (`internal/humanauth`)

1. **Code d'enregistrement** : `bambud enroll` (terminal de l'humain) affiche un code de 8 caractères (alphabet sans `0 O 1 I`), **usage unique, 10 min**, stocké **haché** (SHA-256). Il n'existe ni outil MCP ni route JSON pour le créer ou le lire.
   - *Canal CLI → service* : la commande **écrit directement dans la base SQLite** partagée (WAL, verrou d'écriture court, aucun passage par le verrou d'instance), le service relit la table à chaque saisie. Choix retenu plutôt qu'une socket/un fichier de commande : aucune surface réseau ou fichier supplémentaire à protéger, aucun port, la protection est celle de la base (droits du compte). Justification : un attaquant qui peut écrire dans la base est déjà dans la catégorie « processus local avec les droits de l'utilisateur » (hors périmètre, §1).
2. **Saisie** : `GET/POST /enroll` (HTML serveur). Après un code valide : cookie `bambu_session` (jeton 256 bits, **stocké haché**), `HttpOnly`, `SameSite=Strict`, `Path=/`, `Secure` si la requête est en TLS, si un proxy **de confiance** (`--trusted-proxies`, défaut loopback) envoie `X-Forwarded-Proto: https`, ou si l'URL publique dont l'hôte correspond au `Host` est en `https://` (`--public-url` accepte une liste), durée 30 jours. Anti-force brute : **par adresse client** (adresse TCP, ou entrée `X-Forwarded-For` la plus à droite qui n'est pas un proxy de confiance) — 6 échecs → verrou de 5 minutes, doublé à chaque récidive jusqu'à 1 h ; **plafond global** plus haut (60 échecs en 5 minutes, donc au moins 10 adresses distinctes) → suspension de tous les enrôlements 1 minute, doublée à chaque déclenchement jusqu'à 15 min. Un attaquant isolé ne peut pas bloquer l'enrôlement des autres. La soumission exige `Sec-Fetch-Site: same-origin` et `Sec-Fetch-Mode: navigate`.
3. **Gestion** : `bambud enroll --list`, `--revoke ID` (préfixe accepté si non ambigu), `--revoke-all` ; `POST /enroll/logout` (page) retire le navigateur courant.
4. **Pages protégées** (`humanauth.Require`) : `/confirm/{id}`, `/stop/{id}`, `/setup/printer/{id}`, `/agents/pair`.
   - `GET` sans cookie valide → 303 vers `/enroll?next=…` (chemin local uniquement).
   - `POST` : cookie valide **et** `Sec-Fetch-Site: same-origin` **et** `Sec-Fetch-Mode: navigate` (en-tête absent = refus) **et** nonce de formulaire à usage unique lié à la page.
5. **`GET /api/session`** → `{enrolled, session?}` (sans secret, sans jeton d'API) pour la web app.
6. **Jeton d'API vs cookie** (interface web) : avec `--api-token`,
   - `/mcp` exige **toujours et seulement** le Bearer (le cookie n'y vaut rien : le modèle n'a pas de cookie, et ne doit pas en gagner un) ;
   - `/api/*` accepte le Bearer **ou** le cookie d'un navigateur enregistré. Par cookie, les lectures (`GET`/`HEAD`) passent ; les **écritures** exigent en plus l'en-tête personnalisé `X-Atelier-Request: 1` (envoyé par `web/src/api/client.ts`, impossible à poser depuis un autre site sans préliminaire CORS, que bambud ne donne jamais) **et** `Sec-Fetch-Site: same-origin` (à défaut d'en-têtes Sec-Fetch — les navigateurs n'en envoient pas en `http://` hors localhost —, un `Origin` identique au `Host`), sinon 403 `csrf` ; la protection cross-origin de Go s'applique en plus. Un Bearer valide n'a pas besoin de ces en-têtes ;
   - la SPA (`GET /`, `/assets/*`, routes client) est servie **sans authentification** : elle ne contient aucune donnée (tout passe par `/api/*`). Sans cookie, elle affiche « Enregistrer ce navigateur » (lien vers `/enroll`) ;
   - les pages HTML humaines (`/confirm`, `/stop`, `/setup`, `/agents/pair`) restent **inchangées** : cookie + navigation de même origine + nonce, ni Bearer ni appel de la SPA ne peuvent les remplacer. **Aucune route d'approbation de lancement ou d'arrêt n'existe sous `/api` ni dans `/mcp`** (test `TestCookieWritesNeedCSRFHeaderAndSameOrigin` : un appel « SPA » à `/stop/…` reste refusé) ;
   - l'URL d'aperçu signée (`/api/assets/<sha>.glb?exp&sig`, HMAC + expiration, vérifiée par le handler) reste lisible sans Bearer pour l'iframe d'un hôte MCP Apps.
   Le cookie vaut donc exactement les droits du Bearer sur `/api/*`, pas davantage : c'est l'humain devant son navigateur enregistré.
7. **URL publique** : `--public-url` (env `BAMBU_PUBLIC_URL`, une **liste** séparée par des virgules est possible) : la première fixe la base des liens remis au modèle (`/confirm`, `/stop`, `/setup`) ; tous les noms d'hôte sont autorisés d'office par le garde Host ; plus jamais `http://[::]:port` (hôte non spécifié → `127.0.0.1`).
8. **Jeton obligatoire** : refus de démarrer sans `--api-token` si l'écoute n'est pas loopback, avec `--require-token` / `BAMBU_REQUIRE_TOKEN`, si `--public-url` désigne un hôte non loopback ou si `--allowed-hosts` contient un nom non loopback. **Derrière un proxy (Funnel, `tailscale serve`, nginx), toujours un jeton.** `/mcp` derrière un proxy loopback fonctionne (le garde Host de bambud remplace le contrôle de loopback du SDK MCP).
9. **Proxys de confiance** : `--trusted-proxies` / `BAMBU_TRUSTED_PROXIES` (défaut : loopback ; `none` : aucun). `X-Forwarded-For` et `X-Forwarded-Proto` ne sont lus que sur une connexion TCP venant de ces adresses ; ailleurs ils sont ignorés (forgeables).
10. **Racines d'import** : refus de démarrer si `BAMBU_IMPORT_ROOTS` contient le dossier de données, ou s'y trouve (sinon import puis téléchargement de la base et de `secrets/`).

## 3. Lancement d'une impression (rappel, voir J7b §2)

`request_print_confirmation` → **l'humain** ouvre `/confirm/{id}` dans un navigateur enregistré, coche « plateau vide » et valide → `start_print`.

- La confirmation est liée (imprimante, empreinte, plateau, matières), à usage unique, expire (10 min) ; son jeton reste **en mémoire** du service.
- **L'accord humain ne vaut que 2 minutes après le clic** ; il est **invalidé après tout `not_sent`** (le jeton n'est pas brûlé, l'humain doit recliquer).
- Le jeton est consommé **juste avant la publication MQTT** (`printer.Start` : fichier distant vérifié → relecture d'état fraîche et contrôle idle → vérification/consommation de la preuve → publication).
- Si la confirmation est consommée mais que le passage en `START_REQUESTED` ne peut pas être enregistré : état terminal sûr **`START_ABORTED_BEFORE_SEND`** (rien n'a été envoyé).
- Aucun outil MCP n'approuve : `confirm_print_from_app` a été **supprimé** ; `get_print_confirmation_view` est en lecture seule (ni jeton ni nonce). Test : `TestNoMCPToolCanApproveAConfirmation` énumère tous les outils (schémas d'entrée sans champ d'approbation, appels forgés, la confirmation reste non approuvée).

## 4. Arrêt d'une impression

`cancel_print` sur une impression en cours **ne renvoie aucun identifiant exécutable** : il **crée une demande d'arrêt** (TTL 10 min, réutilisée tant qu'elle est valide) et renvoie `stop_url` (`/stop/{id}`). L'imprimante n'est arrêtée que lorsque l'humain valide cette page (navigateur enregistré + nonce). L'identifiant n'est qu'un localisateur : sans le cookie, il ne sert à rien. Les tables en mémoire `stops`, `pages` (nonces), `approv`, `consumedBy` ont un TTL et sont purgées (à l'émission et chaque minute).

## 5. Imprimantes : hôte, certificat, code d'accès

- `add_printer` n'accepte plus `insecure_reason`, `allow_start_from_finish` ni `pinned_sha256`. Il n'y a **aucun moyen de désactiver la vérification TLS** par MCP/API. `allow_start_from_finish` est un réglage de code de confiance (non validé sur le matériel).
- **Hôte** : adresse privée (RFC 1918), de lien local (169.254/16, fe80::/10), ULA (fc00::/7) ou nom en `.local`. Loopback et adresses publiques refusés (le serveur ne doit pas être détourné vers un tiers), sauf option serveur `--allow-any-printer-host`.
- **Épinglage du certificat** : sur `/setup/printer/{id}`, le **serveur** lit le certificat présenté par l'imprimante (`FetchCertInfo`, rien n'est envoyé), l'**affiche** (empreinte SHA-256, CN) avec le **numéro de série attendu** (avertissement si le CN ne correspond pas, ou si le certificat a changé depuis l'épinglage). L'humain confirme **en saisissant le code d'accès** : l'empreinte épinglée est exactement celle affichée (liée au nonce côté serveur ; un champ forgé dans le formulaire est ignoré). Tant que rien n'est épinglé, **aucune connexion** n'est tentée.
- **Changement d'hôte** (`UpdatePrinterHost`) : l'empreinte épinglée est effacée → ré-épinglage obligatoire.
- Le code d'accès n'est jamais une entrée MCP/JSON ; coffre chiffré (DPAPI + AES-256-GCM) ; jamais journalisé.

## 6. Agent Windows

- **Appairage** : le jeton d'appairage ne se crée plus par `POST /api/agents/pairing` (route supprimée, répond 410). Seulement par `bambud agent pair` (CLI humaine, affiché sur le terminal) ou par la page `/agents/pair` d'un navigateur enregistré. Usage unique, 15 min, haché.
- **Transport** : l'agent accepte `--server-fingerprint` (empreinte SHA-256 du certificat TLS du serveur, épinglage) ; avertissement explicite si le serveur est joint en `http://` hors loopback (secret et fichiers en clair). Client HTTP avec délais (connexion 10 s, TLS 10 s, en-têtes 2 min, JSON 60 s, morceau 10 min).
- **Transferts** : format contrôlé selon le type de travail (sorties `slice` : `.gcode.3mf`/`.3mf` ; import : STL, OBJ, 3MF, STEP — signature vérifiée avant publication dans le magasin), tailles maximales par type (STL/OBJ/STEP 512 Mio, 3MF 1 Gio), verrou par (agent, sha), `.part` complet finalisé à la reprise, `.part` abandonnés purgés (24 h) et supprimés à la fin du travail.
- Gardes d'état (`Complete`, `CancelTask`, `Requeue`) avec contrôle de `RowsAffected` ; `Registry.Run` démarré par `Core.Run`, imports en arrière-plan avec contexte d'arrêt, attendus (`Shutdown`) avant la fermeture de la base.

## 7. Autres durcissements

- **Idempotence** : clés composées `printJobID:kind:clé` (dépôt, lancement) ; rejeu avec d'autres paramètres refusé (`clé d'idempotence déjà utilisée avec d'autres paramètres`).
- **Réconciliation** : `not_started` seulement si l'imprimante est idle (ou finie sur un autre travail) dans un rapport **complet** postérieur à la publication + délai de stabilisation (10 s), sans commande acceptée depuis ; sinon `unknown`. Correspondance de fichier **exacte** (plus de `Contains`).
- `/api/previews/{sha}` ne sert que des blobs enregistrés comme aperçus PNG.
- `GET /api/files/{id}/download` et `POST /api/export` ne servent que des originaux du stockage par empreintes (fichiers copiés, modèle hors corbeille) : aucun chemin du disque n'est lisible par cette voie, les noms d'entrée de l'archive sont assainis, le sha256 est recalculé à l'écriture (une divergence laisse une archive invalide plutôt que fausse). Elles restent derrière le jeton d'API (ou le cookie d'un navigateur enregistré) et le garde Host / cross-origin comme tout `/api`.
- L'interface web n'ajoute aucune route d'approbation : lancement et arrêt passent toujours par `/confirm` et `/stop` (navigateur enregistré) ; la réconciliation d'un lancement incertain est en lecture seule et n'envoie jamais de commande de lancement.
- `EnsureModels` ne fige plus un échec (pas de `sync.Once`).
- `get_print_status` n'est plus annoncé en lecture seule (il met l'impression à jour).

## 8. Limites assumées

- Un processus local avec les droits de l'utilisateur est hors périmètre (§1).
- Ce n'est pas une preuve cryptographique de présence humaine : c'est une barrière qui sort la décision du canal que contrôle le modèle.
- Le cookie `Secure` n'existe qu'avec TLS direct, `X-Forwarded-Proto: https` d'un proxy de confiance ou une URL publique en `https://` correspondant au `Host` (bambud ne termine pas TLS lui-même : mettre un proxy).
- Navigateurs sans `Sec-Fetch-*` (très anciens) : les `POST` des pages humaines sont refusés.
- Un navigateur enregistré garde sa session 30 jours ; révoquer avec `bambud enroll --revoke`.
- Pas encore de rotation du jeton d'API, pas de TLS natif côté serveur.

## 9. File d'attente, bobines et AMS (chantier E)

- **La file ne lance jamais.** `queue_print`, `list_print_queue`, `reorder_queue`, `remove_from_queue` et le service `internal/filament` préparent seulement : attente de la découpe, dépôt du fichier de l'élément de tête (clé d'idempotence `queue-<id>`), demande d'une confirmation (`request_print_confirmation`, qui n'approuve rien), notification. Le démarrage de chaque impression passe par `/confirm/{id}` (navigateur enregistré, nonce, case « plateau vide ») **pour chaque impression**, y compris la suivante après la fin d'une impression : une file n'enchaîne jamais seule.
- Garde-fous testés : `TestQueueNeverStartsAutomatically` (trois impressions en file, le simulateur ne reçoit qu'une commande `project_file` par clic humain, jamais davantage), `TestQueueNeverStartsOrApproves` (le code de `internal/filament` ne contient aucun appel de lancement, d'approbation, d'arrêt ni de consommation de jeton), `TestHTTPRoutes` (aucune route de lancement ou d'approbation sous `/api/print-queue` ni `/api/spools`) ; `TestNoMCPToolCanApproveAConfirmation` couvre aussi les nouveaux outils (aucun champ d'approbation dans leurs schémas).
- Le lien de confirmation affiché par la file expire comme les autres (10 minutes) ; « nouveau lien » redemande une confirmation, jamais un accord.
- **L'affectation matière est liée à l'accord** (`bindingSlot` = `f<n>><emplacement>`) : un changement d'affectation après le clic invalide la confirmation (`TestConfirmationBindsMaterialAssignment`).
- **L'alerte de manque est informative.** Elle s'ajoute au rapport de validation (`filament_short_<n>`) et à la page de confirmation, sans bloquer : un inventaire faux ne doit pas empêcher une impression légitime ; l'humain voit l'alerte avant de cliquer.
- **Saisie humaine protégée** : la lecture de l'AMS ne modifie que les données observées, l'étiquette RFID (si vide) et un restant qui n'a pas été saisi (`remaining_source` différent de `human`). Les champs saisis sont listés dans `human_fields`.
- Les points d'accroche `Printing.AddJobEndHook` et `SetFilamentCheck` n'ont aucun pouvoir sur le lancement : ils observent la fin d'une impression (décompte) ou ajoutent des alertes.
