# Contrôle LAN d'une imprimante Bambu (P1S en priorité) — consultée le 2026-11-06

Légende : [V] fait vérifié · [D] déduction · [?] inconnue. Aucune imprimante n'a été contactée ; tout provient de documentation communautaire / presse / wiki.
Avertissement : la doc Bambu officielle (wiki.bambulab.com) renvoie HTTP 402 au fetch automatisé ; ses propos sont cités via des citations de presse/forums.

## 1. Modes et Authorization Control (firmware 2025)
- [V] Chronologie (https://bambuhub.net/articles/bambu-connect-explained, tiers) : 16 jan 2025 annonce de l'« Authorization Control System » + Bambu Connect ; 17 jan bêta X1 01.08.03.00 ; 20 jan annonce du Developer Mode ; 12 mar 2025 stable X1 01.08.05.00 ; juin 2025 stable P1/A1.
- [V] Firmwares où existent Developer Mode + restrictions (https://help.simplyprint.io/en/article/bambu-lab-lan-only-mode-and-developer-mode-how-to-enable-xa0hch/) : X1 ≥ 01.08.03.00, A1 ≥ 01.05.00.00, **P1 ≥ 01.08.02.00**, H2D ≥ 01.01.00.01, P2 série dès l'origine. Avant ces versions : pas de Developer Mode ni de restrictions (LAN ouvert).
- [V] Citation de la page firmware Bambu relayée (WebSearch, 2026-11-06) : « Developer Mode (Available in "LAN Only" Mode). • When enabled, Authorization Control will be disabled, allowing the printer to accept all control instructions without verification. » Wiki Bambu : le Developer Mode « will not allow the printer to be connected to Bambu Cloud, so it is exclusively working in LAN Mode ». Bambu ne fournit pas de support pour MQTT/intégrations.
- [V] Ce que l'Authorization Control bloque sur firmware à jour, hors Developer Mode (bambuhub) : démarrage d'impression par le réseau (cloud ET LAN), contrôle mouvement/ventilateurs/buse, config et calibration AMS, caméra distante, mises à jour firmware. Impression depuis carte SD hors réseau non affectée. Le forum Bambu (https://forum.bambulab.com/t/explaining-the-auth-system-in-laymans-terms/136629/21) dit que l'envoi d'**informations d'état** reste non authentifié.
- [V] Developer Mode (bambuhub + presse) : rétablit MQTT, flux vidéo et FTP pour les tiers, vérification désactivée. Coûts : plus de Cloud ni de Bambu Handy, pas de support.
- [V] Activer (P1S, SimplyPrint) : Réglages > WLAN/Réseau > activer **LAN Only Mode**, redémarrer l'imprimante, puis l'option **Developer Mode** apparaît dans le même menu ; le code d'accès est affiché dans Réglages > WLAN/Réseau. Le code d'accès peut changer après une mise à jour firmware.
- [D] Exigences pour notre MCP : LAN Only + Developer Mode **obligatoires** pour `project_file`, pause/resume/stop, `gcode_line`, FTP upload. Le simple suivi d'état MQTT fonctionnerait peut-être en LAN Only sans Developer Mode [?] — non confirmé pour la connexion MQTT elle-même (le forum ne parle que de « statut »). Alternative officielle sans Developer Mode : Bambu Connect (app relais, URL interface) — hors scope headless [D].

## 2. MQTT (OpenBambuAPI : https://github.com/Doridian/OpenBambuAPI/blob/main/mqtt.md, lu 2026-11-06)
- [V] Broker local : `mqtts://{IP}:8883` (TLS), utilisateur **`bblp`**, mot de passe = code d'accès LAN (`dev_access_code`). [D] certificat auto-signé de l'imprimante : client à configurer sans vérification stricte (ou avec la CA Bambu).
- [V] Topics : `device/{SERIAL}/report` (imprimante → client, incl. réponses) ; `device/{SERIAL}/request` (client → imprimante). Les `#` wildcards marchent.
- [V] Format requête : `{"print": {"sequence_id": "0", "command": "<CMD>", ...}}` (incrémenter sequence_id) ; types de racine `print`, `info`, `pushing`, `system`, `camera`, `xcam`, `upgrade`. Réponse : même `sequence_id` + `result`, parfois `reason`.
- [V] Commandes :
  - `{"pushing":{"sequence_id":"1","command":"pushall","version":1,"push_target":1}}` : état complet. X1 envoie l'objet complet à chaque fois, **P1 n'envoie que les changements** → pushall nécessaire ; « refrain from executing this command at intervals less than 5 minutes on the P1P ».
  - `{"info":{"command":"get_version"}}` : modules et versions firmware.
  - `print.pause|resume|stop` avec `"param":""` (QoS 1). `print.print_speed` param "1".."4" (silent, standard, sport, ludicrous). `print.gcode_line` param = G-code (`\n`). `print.gcode_file` param = chemin absolu sur l'imprimante.
  - `print.project_file` (exemple de la doc) :
    ```json
    {"print":{"sequence_id":"0","command":"project_file","param":"Metadata/plate_1.gcode",
     "project_id":"0","profile_id":"0","task_id":"0","subtask_id":"0","subtask_name":"",
     "file":"","url":"file:///mnt/sdcard/xxx.gcode.3mf","md5":"",
     "timelapse":true,"bed_type":"auto","bed_levelling":true,"flow_cali":true,
     "vibration_cali":true,"layer_inspect":true,"ams_mapping":"","use_ams":false}}
    ```
    Doc : `"url": "file:///mnt/sdcard"` (ou `ftp:///myfile.3mf`, `ftp:///cache/…`), ids toujours "0" en local, `bed_type` "auto" en local. [D] pour une P1S : `url` = `ftp:///fichier.gcode.3mf` ou `file:///mnt/sdcard/fichier.gcode.3mf` selon l'emplacement du fichier après FTPS (variantes selon firmware ; **[?] à valider sur matériel**).
  - `ams_mapping` : tableau de 5 entiers (-1 = inutilisé, valeurs 0–3 = slot AMS) ; remplissage depuis la droite ; avec `use_ams:true`. « The printer will just pause and won't start if the mapping is not correct. »
  - Autres : `print.skip_objects`, `print.ams_*`, `system.ledctrl` (chamber_light/work_light), `system.get_access_code`, `camera.ipcam_record_set`, `xcam.xcam_control_set`, `upgrade.*`.

## 3. Rapport d'état (`print` / `command: "push_status"`)
[V] Exemple IDLE de la doc : `gcode_state:"IDLE"`, `mc_percent:0`, `layer_num:0`, `nozzle_temper:25.0`, `nozzle_target_temper`, `bed_temper`, `bed_target_temper`, `chamber_temper`, `print_error:0`, `mc_print_error_code:"0"`, `hms:[]`, `mc_remaining_time`, `gcode_file`, `subtask_name`, `task_id`, `cooling_fan_speed`, `big_fan1_speed` (aux), `big_fan2_speed` (chambre), `heatbreak_fan_speed`, `spd_lvl`, `stg_cur`, `fail_reason`, `lights_report[]`, `wifi_signal`, `sdcard`, `ipcam{}`, `nozzle_diameter`, `ams{}` (tray_type, tray_color, remain, humidity, temp), `vt_tray` (bobine externe id 254), `tray_now` (255 aucun, 254 externe, sinon ams_id*4+tray_id), `s_obj`, `online`, `upload`.
- `total_layer_num` : [V] utilisé en pratique (non présent dans l'exemple IDLE) ; [D] apparaît pendant une impression.
- Valeurs de `gcode_state` [V] (liste exhaustive du client ha-bambulab, `GCODE_STATE_OPTIONS`, https://github.com/greghesp/ha-bambulab) : `failed, finish, idle, init, offline(synthétique), pause, prepare, running, slicing` (casse majuscule côté imprimante : IDLE, PREPARE, RUNNING, PAUSE, FINISH, FAILED).
- `hms` : [V] tableau (vide dans l'exemple). [D] chaque entrée `{attr, code}` entiers → code HMS à 16 hex ; le contenu n'est pas documenté dans OpenBambuAPI (ha-bambulab a une table de textes, non lue en détail). `print_error` : entier (0 = aucune), voir table `print_error` de ha-bambulab (ex. pauses « paused_user », « paused_filament_runout »…).

## 4. FTPS (https://github.com/Doridian/OpenBambuAPI/blob/main/ftp.md)
- [V] `ftps://{IP}:990`, **TLS implicite** (pas STARTTLS), utilisateur `bblp`, mot de passe = code d'accès. Répertoires/caveats : non documentés dans la page.
- [D] Racine = carte SD ; déposer le `.gcode.3mf` puis lancer `project_file` avec l'URL correspondante. Client à choisir : support FTPS implicite + réutilisation de session TLS (problème classique avec certains serveurs) [?].

## 5. Caméra (https://github.com/Doridian/OpenBambuAPI/blob/main/video.md)
- [V] X1 et P2S : `rtsps://{IP}:322/streaming/live/1`, auth `bblp` / code d'accès. H2S et H2D : même URL mais désactivée par défaut (`ipcam.rtsp_url = "disable"`), à activer sur l'écran (« LAN Mode Liveview »).
- [V] **A1 et P1 (donc P1S)** : pas de RTSP ; serveur TCP/TLS `{IP}:6000`, JPEG 1280x720 ; paquet d'auth de 80 octets (payload 0x40, type 0x3000, flags 0, 0, puis user 32 o + mot de passe 32 o, ASCII complété de zéros) ; chaque trame : en-tête 16 o (taille payload LE, itrack 0, flags 1, 0) + JPEG (FFD8…FFD9).
- [D] Pour un viewer MCP App : relais côté serveur MCP (JPEG → MJPEG/WebSocket) car l'iframe ne peut pas parler TCP ni TLS vers l'imprimante.

## 6. Inconnues à lever sur matériel (au moment du branchement)
1. Firmware exact de la P1S et disponibilité du Developer Mode ; l'authentification MQTT fonctionne-t-elle en LAN Only sans Developer Mode (lecture seule) ?
2. Forme exacte de `url`/`param` de `project_file` pour une P1S selon firmware, et nom du dossier SD après FTPS.
3. Format réel de `hms` et de `print_error` sur le firmware courant ; présence de `total_layer_num`.
4. Comportement du code d'accès après mise à jour firmware.
5. Évolution de la politique Bambu en 2026 (bambuhub cite des événements avril–mai 2026 : retrait d'un fork OrcaSlicer, déclaration cloud du 7 mai) : non recoupé avec une source primaire.
