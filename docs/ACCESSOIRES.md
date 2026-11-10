# Pilotage des accessoires de l'imprimante (N4)

Date : 2026-11-10. Éclairage (chambre, tête), ventilateurs (pièce, auxiliaire, chambre) et vitesse de l'impression **en cours**. Ce chemin **ne lance, n'arrête, ne met en pause ni ne reprend jamais une impression** et n'envoie **jamais de G-code libre**.

**Statut : validé contre le simulateur seulement.** Les formats de commande ci-dessous viennent de la documentation communautaire (OpenBambuAPI, ha-bambulab, comportement de Bambu Studio) ; aucune imprimante réelle n'a été contactée. Voir « À prouver sur la vraie machine ».

## 1. Outils MCP

| Outil | Entrée | Effet |
|---|---|---|
| `set_printer_light` | `printer_id?`, `light` = `chamber` \| `work`, `on` (booléen) | allume ou éteint la lumière, à tout moment |
| `set_printer_fan` | `printer_id?`, `fan` = `part` \| `aux` \| `chamber`, `percent` 0 à 100 | règle un ventilateur |
| `set_print_speed_level` | `printer_id?`, `level` = `silent` \| `normal` \| `sport` \| `ludicrous` (ou 1 à 4) | règle la vitesse de l'impression en cours |

- `printer_id` est facultatif quand une seule imprimante est enregistrée (sinon `list_printers`).
- Écriture non destructive, idempotente (poser une valeur), `openWorldHint` vrai (effet physique) ; `Title` et `outputSchema` fournis (`AccessoryResult` : `applied`, `message`, `accessories`, `available_accessories`).
- Aucun nom d'outil ne contient `approve`, `start`, `launch`, `stop`, `resume`, `cancel` (test).
- **Pas d'approbation humaine** : l'effet est réversible, sans risque thermique ni mécanique ; le modèle ne peut ni chauffer ni déplacer. Ce choix est cohérent avec docs/SECURITY.md (l'accord humain protège le lancement, l'arrêt, la reprise, le chargement et la suppression de fichiers, aucun de ces gestes n'est ici).
- Lecture de l'état : `get_printer_status` renvoie, en champs **facultatifs**, `accessories` (lumières, ventilateurs en %, niveau de vitesse tels que rapportés par l'imprimante) et `available_accessories` (ce que le modèle possède). `list_printer_models` renvoie `accessories` par modèle ; `get_capabilities` annonce la fonction `printer_accessories`.

## 2. Commandes MQTT (liste blanche)

Topic `device/{serial}/request`, QoS 1, une seule publication (jamais de renvoi).

| Accessoire | Message | Source |
|---|---|---|
| Lumière | `{"system":{"sequence_id":"…","command":"ledctrl","led_node":"chamber_light"\|"work_light","led_mode":"on"\|"off","led_on_time":500,"led_off_time":500,"loop_times":0,"interval_time":0}}` | OpenBambuAPI (`ledctrl`), ha-bambulab (lumières) |
| Ventilateur | `{"print":{"sequence_id":"…","command":"gcode_line","param":"M106 P1\|P2\|P3 S<0-255>\n"}}` ; P1 = pièce, P2 = auxiliaire, P3 = chambre | OpenBambuAPI (`gcode_line`), usage de Bambu Studio / ha-bambulab pour `M106 P1/P2/P3` |
| Vitesse | `{"print":{"sequence_id":"…","command":"print_speed","param":"1\|2\|3\|4"}}` ; 1 silencieux, 2 normal, 3 sport, 4 ludicrous | OpenBambuAPI (`print_speed`) |

Lecture dans les rapports `push_status` : `lights_report` (`node`, `mode`), `cooling_fan_speed` / `big_fan1_speed` / `big_fan2_speed` (cran 0 à 15, converti en %, arrondi à l'entier), `spd_lvl` (1 à 4), `spd_mag` (pourcentage de vitesse). Un champ non transmis reste absent : rien n'est deviné.

Conversion : `S = (pourcentage × 255 + 50) / 100` (0 donne 0, 50 donne 128, 100 donne 255).

### Liste blanche stricte (`internal/printer/accessories.go`)

- **Un seul point d'entrée par accessoire** : `SetLight`, `SetFan`, `SetSpeed` (interface optionnelle `printer.AccessoryController`, que l'adaptateur Bambu LAN implémente).
- Toute commande passe par `ValidateAccessoryPayload` **avant** la publication : seules les trois formes ci-dessus, avec exactement leurs champs, passent. Sont refusés (`ErrAccessoryNotAllowed`) : `pause`, `resume`, `stop`, `project_file`, `ams_change_filament`, tout `gcode_line` qui n'est pas `M106 P1|P2|P3 S0..255\n` (donc ni `M104`, `M140`, `G28`, ni plusieurs lignes), toute clé supplémentaire, `print_speed` hors 1 à 4, `led_mode` autre que `on`/`off`, `led_node` autre que les deux lumières.
- Bornes : pourcentage 0 à 100 (refusé sinon, avant tout envoi), niveau 1 à 4.
- Le transport « agent » (imprimante servie par un agent local) ne relaie pas ces commandes : refus « non pris en charge ».

## 3. Règles d'état

| Accessoire | Autorisé | Refusé |
|---|---|---|
| Lumière | à tout moment (connexion active) | imprimante non connectée |
| Ventilateur | repos, impression, pause, fin | **préparation** (mise à niveau, calibration : la machine exécute son propre G-code) |
| Vitesse | préparation, impression, pause | tout le reste (« la vitesse ne se change que pendant une impression ») |

Pendant une impression, un `M106` du G-code de la pièce peut reprendre la main sur un réglage de ventilateur au changement de couche suivant : c'est le comportement de la machine, pas une erreur.

## 4. Capacités par modèle (`internal/printer/accessories.go`, table `accTable`)

Un accessoire absent est refusé avec « non disponible sur ce modèle » **avant tout contact** avec l'imprimante. Un modèle absent de la table est entièrement refusé (rien n'est deviné).

| Modèle | Lumière chambre | Lumière tête | Ventilateur pièce | Auxiliaire | Chambre | Vitesse |
|---|---|---|---|---|---|---|
| P1P | non | non | oui | oui | non | oui |
| P1S | oui | non | oui | oui | oui | oui |
| X1 / X1C / X1E | oui | non | oui | oui | oui | oui |
| A1 / A1 mini | non | oui | oui | oui | non | oui |
| P2S | oui | non | oui | oui | oui | oui |
| H2D / H2D Pro / H2S / H2C | oui | oui | oui | oui | oui | oui |

La table est **non vérifiée sur du matériel** : elle s'appuie sur les fiches techniques publiques et les fonctions par modèle de ha-bambulab. Si une machine la contredit, corriger `accTable` (un test par modèle fige la table actuelle, `TestAccessoriesForModels`).

## 5. Audit

Chaque commande **envoyée** écrit `printer.accessory_set` (entité `printer`, identifiant de l'imprimante) avec l'acteur (`mcp` pour les outils, `web` pour l'interface) et `{accessory, value, outcome}` : `light:chamber` / `fan:aux` / `speed`, `on`/`off`, pourcentage ou nom de niveau, `ok` / `unconfirmed` / `failed`. Aucun secret. Un refus avant envoi (modèle sans l'accessoire, valeur hors bornes) n'envoie rien et n'écrit pas d'entrée.

## 6. Interface web

Panneau « Accessoires » dans la carte de chaque imprimante (page Imprimantes), visible quand l'imprimante est joignable et a au moins un accessoire connu : boutons Allumer / Éteindre, ventilateurs en paliers (Arrêt, 25, 50, 75, 100 %), vitesse (Silencieux, Normal, Sport, Ludicrous). États écrits en texte (« allumée », « 50 % », « Sport (niveau 3) »), valeur courante en `aria-pressed`, vitesse désactivée et expliquée hors impression. Aucun bouton Lancer, Arrêter, Pause ni Reprendre. Routes : `POST /api/printers/{id}/accessories/light|fan|speed` (mêmes règles, acteur `web`). Pas de carte MCP dédiée (le mécanisme n'est pas trivial).

## 7. Tests

- `internal/printer/accessories_test.go` : bornes, formes des commandes, liste blanche (arrêt, pause, reprise, `project_file`, G-code libre, clés en trop), table par modèle, lecture des rapports.
- `internal/printer/accessories_sim_test.go` : cas nominal avec le simulateur (lumières, ventilateurs), bornes sans envoi, vitesse refusée au repos et acceptée en impression, ventilateur refusé en préparation, seules des commandes d'accessoires reçues (jamais `stop`/`pause`/`resume`, un seul `project_file` : celui du lancement humain).
- `internal/printing/accessories_e2e_test.go` : outils MCP de bout en bout (schémas, annotations, noms), P1S nominal, P1P sans lumière (refus clair, aucun `ledctrl`), HTTP, audit sans secret, preuve que les outils n'envoient ni démarrage, ni arrêt, ni pause, ni reprise, ni G-code hors liste blanche.
- Garde-fous : `TestNoMCPToolCanApproveAConfirmation` (noms, champs, commandes reçues), `TestAccessoryToolsConformToSpec`, `TestAccessoryToolsTakeNoSecret`.
- Simulateur (`internal/printer/sim/accessories.go`) : accepte `ledctrl`, `M106 P1/P2/P3` (tout autre `gcode_line` est refusé), `print_speed` (pendant une impression seulement) ; reflète `lights_report`, les crans de ventilateur, `spd_lvl`, `spd_mag` ; expose `GcodeLines()` et `Accessories()` aux tests.

## 8. À prouver sur la vraie machine

- Le message `ledctrl` et sa réponse (objet `system`), y compris le comportement du mode `flashing`.
- `M106 P3` sur la chambre de P1S / X1 / H2 (certaines machines ignorent P3) et le rapport `big_fan2_speed`.
- `print_speed` : valeurs 1 à 4 et `spd_lvl` rapporté, refus hors impression.
- Le rapport `lights_report` sur P1P / A1 (présence de `work_light`).
- La table des modèles (surtout A1 mini, P2S, H2*).
- Le format `fan_gear` des firmwares récents (non lu : seuls les trois champs de cran sont exploités).
