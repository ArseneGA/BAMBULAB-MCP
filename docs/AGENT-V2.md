# Agent local v2 — protocole `/agent/v2` et imprimante « via agent »

Première brique de la cible SaaS (serveur cloud + agent local). **Rien ne change pour une installation existante** : toute imprimante reste en transport `direct` (bambud parle lui-même à l'imprimante : MQTT TLS 8883, FTPS 990, caméra 6000) tant qu'on ne l'a pas basculée explicitement. Aucune variable d'environnement nouvelle, aucun outil MCP renommé ou ajouté, protocole `/agent/v1` (agent Windows de l'atelier) inchangé.

## 1. Pourquoi

Dans la cible SaaS, le serveur n'est pas sur le réseau de l'imprimante. Un petit programme local (Raspberry Pi, PC Linux ou Windows) :

- ouvre une connexion **sortante** vers le serveur (rien à ouvrir chez l'utilisateur) ;
- parle seul à l'imprimante et **garde seul le code d'accès LAN**, qui n'est jamais transmis au serveur ;
- ne lance une impression que sur une **autorisation signée** par le serveur, qu'il revérifie.

## 2. Architecture

```
 Claude / navigateur ─► bambud (printing.Service)
                         │  printer.Link (interface)
                         ├─ DirectLink  (défaut)  ──► imprimante   [MQTT 8883 / FTPS 990 / caméra 6000]
                         └─ HubLink  (link=agent)
                              │  Hub (internal/agentv2) : file d'ordres, télémétrie, fichiers, signature
                              ▼  HTTP(S) /agent/v2, connexions SORTANTES de l'agent
                        bambu-agent printer-link (Runner) ──► imprimante (LAN)
```

- `internal/printer/link.go` : `printer.Link` = `Adapter` (état, dépôt, lancement, pause/reprise/arrêt, abonnement) + `Reconciler` + `Snapshot` (caméra). `DirectLink` enveloppe l'adaptateur Bambu LAN tel quel ; `agentv2.HubLink` relaie vers l'agent.
- Choix **par imprimante** : champs `link` (`direct` | `agent`, absent = `direct`) et `agent_id` du document de l'imprimante.
- `internal/agentv2` ne dépend ni de `agentlink`, ni de `core`, ni de `printing` : l'authentification et l'appairage sont injectés (`Authenticator`, branché sur le registre existant par `internal/httpapi/agentv2.go`).

## 3. Activer le mode agent

1. **Jeton d'appairage** (mécanisme existant, usage unique) : `bambud agent pair`, ou la page `/agents/pair` d'un navigateur enregistré.
2. **Clé de signature du serveur** (à comparer hors bande) : `GET /api/agent-v2/server-key` → `key_id`. Elle est créée à la première utilisation, dans `data/secrets/agent-v2-ed25519.key`.
3. **Sur la machine locale** (même réseau que l'imprimante) :
   ```
   # l'empreinte du certificat de l'imprimante, à comparer avec celle de l'imprimante :
   bambu-agent printer-link --printer-host 192.168.1.50 --discover-pin
   # code d'accès LAN dans un fichier (droits 0600), jamais en argument :
   bambu-agent printer-link --server https://bambu.example --token pair-… --server-key-id <key_id> \
       --printer-id prn_… --printer-host 192.168.1.50 --printer-serial 01P00A… \
       --access-code-file /etc/bambu/access-code --pin-sha256 <empreinte>
   ```
   Le premier démarrage appaire (jeton consommé, secret d'agent et clé du serveur enregistrés dans `<state-dir>/link.json`, droits 0600) puis tourne. Les démarrages suivants : les mêmes options sans `--token`. `--server-fingerprint` épingle le certificat TLS du serveur ; `bambu-agent printer-link -h` liste les options.
4. **Basculer l'imprimante** : `PUT /api/printers/{id}/link` avec `{"link":"agent","agent_id":"agt_…"}` (`agent_id` : voir `GET /api/agents`). Retour en direct : `{"link":"direct"}`. La bascule ferme l'ancien adaptateur et n'envoie rien à l'imprimante ; le code d'accès éventuellement enregistré côté serveur n'est ni lu, ni effacé, ni transmis à l'agent.

Compilation (Go pur, sans cgo) : `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./cmd/bambu-agent` (Raspberry Pi), `linux/amd64`, `windows/amd64`.

**Kill-switch** : `--read-only`, ou création du fichier `<state-dir>/READ_ONLY` (ou celui de `--read-only-file`), relu à chaque ordre : `touch` / `rm` sans redémarrer. Voir § 6.

## 4. Protocole `/agent/v2`

HTTP + JSON, préfixe `/agent/v2`. Authentification : `Authorization: Bearer <agent_id>.<secret>` (celle de v1 : même table d'agents, même révocation). La garde HTTP de bambud laisse passer `/agent/v2/*` comme `/agent/v1/*` (l'agent n'est pas un navigateur). `ProtocolVersion = 2`.

| Route | Rôle |
|---|---|
| `POST /pair` | `{token, name, host, agent_version, protocol:2, printer_id}` → `{agent_id, secret, server_key, server_key_id, …}`. Le jeton est celui de v1 (usage unique). **La clé publique du serveur est épinglée par l'agent.** |
| `GET /orders/next?printer_id=&wait=` | **Long-poll sortant** : un ordre (200) ou rien (204). Un ordre n'est remis qu'**une fois**. |
| `POST /orders/{id}/result` | Résultat d'un ordre (`state`, `code`, `message`, …). |
| `POST /telemetry` | État de l'imprimante (`printer.Status`) + `age_ms` (âge mesuré par l'agent : le serveur recale `UpdatedAt` sur SON horloge) + `read_only`. Toutes les 5 s. |
| `GET /files/{id}` | Fichier à déposer, servi **par morceaux** (`Range`) ; l'agent réessaie chaque morceau. |

Ordres (`kind`) : `put_file`, `start`, `control` (`pause|resume|stop`), `snapshot`, `reconcile`, `status`.

**Dépôt de fichier** (`put_file`). Le serveur copie le flux en transit en le hachant (taille et SHA-256 vérifiés), puis émet l'ordre ; l'agent tire le fichier par morceaux, **revérifie taille et empreinte**, le dépose en FTPS (idempotent par nom + taille, comme en direct), vérifie à nouveau, et inscrit dans son **registre des dépôts** (`deposits.json`) l'empreinte et le nom distant.

**Lancement** (`start`). Voir § 5. **Réconciliation** (`reconcile`) : l'agent relit l'état et répond `started | not_started | unknown` ; il n'émet jamais de commande de lancement.

Résultats d'un lancement : `started` (PREPARE/RUNNING observé) ; `rejected` (l'imprimante a répondu non) ; `refused` (l'agent a refusé AVANT toute commande : `read_only`, `bad_signature`, `expired`, `not_yet_valid`, `replay`, `wrong_target`, `file_unknown`, `hash_mismatch`, `busy`, `not_ready`, …) ; `unknown` (commande peut-être partie, étape donnée).

## 5. L'autorisation signée de lancement

Le serveur signe (Ed25519, domaine `bambu-atelier/agent-v2/start/1`) la charge utile JSON :

`agent_id`, `printer_id`, `sha256` du fichier, `plate`, `use_ams` + `ams_mapping`, options (`timelapse`, `bed_leveling`, `flow_cali`, `vibration_cali`, `layer_inspect`, `bed_type`), `plate_clear`, `issued_at`, `expires_at` (**2 minutes**), `id` (**nonce** de 128 bits, usage unique), `key_id`.

Côté serveur (`HubLink.Start`), dans cet ordre, **avant** toute signature : preuve de confirmation vérifiée (`printer.CheckProofBinding`, la même qu'en direct) ; agent en ligne et pas en lecture seule ; état frais et lançable (`printer.CheckLaunchable`) ; **puis** consommation de la confirmation humaine à usage unique ; puis signature, un seul ordre.

Côté agent (`Runner.doStart`), tout est revérifié :

1. kill-switch local (prioritaire) ;
2. signature avec la clé **épinglée** ; forme ; fenêtre ≤ 2 min 30 ; fraîcheur (tolérance d'horloge 30 s) ;
3. cible : cet agent, cette imprimante ; « plateau vide » attesté ; plateau et matières valides ;
4. le fichier a été **déposé par cet agent** (registre) ;
5. **empreinte SHA-256 du fichier présent sur l'imprimante relue en FTPS** (option `--skip-remote-hash` pour ne contrôler que la taille : déconseillé) ;
6. fraîcheur revérifiée, puis **nonce inscrit sur disque (journal `nonces.log`, synchronisé)** avant la commande : un rejeu est impossible, même après redémarrage ;
7. **une** commande `project_file` (l'adaptateur local refait ses garde-fous : état frais, pas de HMS ni d'erreur en cours, une seule commande) ; **jamais de nouvelle tentative**.

`START_UNKNOWN` est conservé : si l'issue est indéterminée, `printer.StartUnknownError` remonte (étape `transport_unacknowledged | transport_acknowledged | accepted`), le service passe l'impression en `START_UNKNOWN`, et seule une réconciliation (lecture d'état) conclut. Si l'ordre n'a jamais été remis à l'agent (hors ligne, pas de long-poll), il est **retiré** et l'erreur dit explicitement « rien n'a été envoyé ».

Journal côté serveur : table `agent_v2_authz` (migration `0260_agent_v2.sql`) : une ligne par autorisation (agent, imprimante, empreinte, plateau, émission, expiration, résultat) + audit.

## 6. Kill-switch « lecture seule »

Local à l'agent, jamais levé par le serveur : `--read-only` (option) ou fichier (`<state-dir>/READ_ONLY` par défaut, `--read-only-file`) relu à chaque ordre. En lecture seule l'agent refuse **tout lancement, tout dépôt de fichier et toute reprise** (`resume`). Il laisse passer l'**arrêt** et la **pause** (gestes de sécurité), l'état, la caméra et la réconciliation. Il annonce son mode en télémétrie : le serveur refuse alors le lancement **avant** de consommer la confirmation.

## 7. Ce qui est prouvé par test (contre le simulateur d'imprimante)

`internal/agentv2/e2e_test.go` (Hub + HTTP `httptest` + Runner + simulateur) et `internal/printing/agentlink_e2e_test.go` (chaîne bambud complète) : appairage (jeton à usage unique, clé attendue, clé qui survit au redémarrage) ; télémétrie recalée sur l'horloge du serveur ; dépôt par morceaux de 64 Kio, octets identiques sur l'imprimante, idempotent ; lancement signé accepté **une fois** (une seule commande reçue par l'imprimante) ; **rejeu refusé** ; **autorisation expirée** et fenêtre trop longue refusées ; **empreinte différente** (fichier altéré, empreinte jamais déposée) refusée ; signature d'une autre clé, charge modifiée, autre imprimante, autre agent, plateau non attesté, ordre sans autorisation refusés ; **lecture seule** refuse lancement, dépôt, reprise (et laisse l'arrêt) ; `START_UNKNOWN` sans renvoi ni réconciliation qui lance ; refus de l'imprimante distingué de l'inconnu ; ordre non remis retiré ; instantané caméra relayé ; journal des nonces durable ; défaut `direct` sans clé ni dossier créés ; code d'accès absent du serveur (données, journaux, audit, réponses d'outils).

## 8. Reste à faire pour le SaaS (hors de cette brique)

- Multi-tenant : comptes, isolation des données, agents et imprimantes par organisation ; autorisations de lancement liées à l'identité de l'humain.
- **Confirmation humaine signée de bout en bout** (clé de l'humain, pas seulement celle du serveur) : aujourd'hui un serveur compromis peut signer des lancements.
- Rotation et révocation de la clé de signature du serveur ; secret d'agent protégé par le magasin du système (DPAPI, TPM, trousseau) ; mise à jour de l'agent ; installateur et service systemd / Windows pour `printer-link`.
- Plusieurs imprimantes par agent ; reprise d'un dépôt interrompu entre deux exécutions de l'agent (aujourd'hui : par morceau, dans l'exécution en cours) ; flux caméra continu et surveillance périodique relayés (aujourd'hui : instantanés) ; RTSP (X1, P2S, H2).
- Découverte de l'imprimante sur le réseau local, interface d'appairage dans la web UI (aujourd'hui : CLI + route `PUT /api/printers/{id}/link`).
- Proxy TLS et épinglage géré (v1 le signale déjà en HTTP clair).
- Validation sur Raspberry Pi et sur la vraie P1S (non faite : simulateur seulement).
