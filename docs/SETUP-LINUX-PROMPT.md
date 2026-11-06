# Prompt à donner à Claude Code sur le serveur Linux (Zorin OS)

Copie tout le bloc ci-dessous dans Claude Code, lancé sur l'ordinateur Zorin.

---

Tu prépares cet ordinateur sous Zorin OS (base Ubuntu) pour qu'il devienne le serveur permanent d'« Atelier Bambu », une plateforme qui gère des fichiers 3D et prépare des impressions pour des imprimantes Bambu Lab (P1S). Le logiciel lui-même (binaire Go `bambud`, worker Python CAO, Bambu Studio en ligne de commande) sera déployé plus tard depuis le dépôt https://github.com/ArseneGA/BAMBULAB-MCP. Ta mission : inventorier la machine, la préparer et prouver que tout fonctionne.

Règles :
- Avant toute action qui modifie le système (paquets, utilisateurs, pare-feu, services, disques, réseau), dis ce que tu vas faire et demande mon accord en une fois par étape. Tu ne formates, ne partitionnes et ne supprimes jamais rien sans accord explicite.
- Ne contacte pas l'imprimante. Ne mets aucun mot de passe, code d'accès ou clé dans un fichier versionné ni dans tes réponses.
- Note tout ce que tu fais dans `~/atelier-setup/RAPPORT.md` (commandes, résultats, versions, problèmes).

Étapes :

1. **Inventaire (lecture seule)** : modèle de la machine, CPU et architecture (`uname -m`, `lscpu`), nombre de cœurs, RAM exacte (`free -h`), disques et espace libre (`lsblk`, `df -h`), version de Zorin et d'Ubuntu de base (`lsb_release -a`), noyau, carte graphique et pilote, adresse IP locale et interface réseau, présence de systemd, de SSH, de Docker. Résume-le dans le rapport sous un titre « Inventaire ».

2. **Système à jour et outils de base** : `apt update && apt upgrade`, puis installe `git curl wget unzip ca-certificates build-essential openssh-server ufw sqlite3 xvfb xauth libgl1 libegl1 libgtk-3-0 libwebkit2gtk-4.1-0 libfuse2t64` (ou `libfuse2` selon la version d'Ubuntu ; adapte les noms de paquets s'ils n'existent pas et note-le).

3. **Utilisateur de service et dossiers** : crée un utilisateur système `atelier` sans connexion interactive, et les dossiers `/opt/atelier` (logiciel), `/var/lib/atelier` (données : base et fichiers 3D), `/var/log/atelier`, avec les bons propriétaires et des droits restreints.

4. **Python et uv** : installe `uv` (méthode officielle de astral.sh) pour l'utilisateur `atelier`, vérifie `uv python install 3.12`, puis vérifie que `cadquery` s'installe dans un environnement de test sous `/opt/atelier/cad-test` (`uv venv`, `uv pip install cadquery`, puis un script qui crée un cube et l'exporte en STEP). Supprime l'environnement de test ensuite. Note la durée et les éventuelles erreurs.

5. **Go (pour compiler sur place si besoin)** : installe la dernière version stable de Go depuis go.dev (pas la version apt, souvent trop ancienne) dans `/usr/local/go`, ajoute-la au PATH, vérifie `go version`.

6. **Bambu Studio en ligne de commande (test headless)** : télécharge depuis https://github.com/bambulab/BambuStudio/releases la dernière AppImage correspondant à la version d'Ubuntu de base (ubuntu 22.04 ou 24.04), place-la dans `/opt/atelier/bambustudio/`, rends-la exécutable. Puis teste un vrai slicing sans écran : génère un cube STL de 20 mm (petit script Python), et lance sous `xvfb-run -a` la commande : `BambuStudio.AppImage --slice 0 --outputdir /tmp/slice-test --export-3mf cube.gcode.3mf cube.stl` en chargeant les profils machine/process/filament de la P1S buse 0,4 et PLA Basic (fichiers JSON dans le dossier `resources/profiles/BBL` de l'AppImage : extrais-la avec `--appimage-extract` pour les trouver ; le CLI exige des presets complets, résous les `inherits` si nécessaire). Vérifie que `result.json` indique un succès et que le `.gcode.3mf` contient `Metadata/plate_1.gcode`. Si ça échoue (erreur `glfwInit`, OpenGL, FUSE…), essaie : extraction de l'AppImage et lancement direct de `AppRun`, `LIBGL_ALWAYS_SOFTWARE=1`, `xvfb-run -s "-screen 0 1280x1024x24"`. Note précisément ce qui marche ou non, la durée du slicing et la mémoire utilisée.

7. **Réseau** : vérifie si l'IP locale est fixe (réservation DHCP sur la box ou IP statique via NetworkManager). Ne change pas la configuration réseau sans accord ; explique-moi les options. Note le nom d'hôte.

8. **Pare-feu** : active `ufw` en autorisant SSH (22) et le port de l'Atelier 7878 uniquement depuis le réseau local (déduis le sous-réseau de l'IP). Rien d'autre d'ouvert vers l'extérieur.

9. **SSH** : vérifie que le serveur SSH tourne. Propose (sans l'appliquer d'office) l'authentification par clé et la désactivation du mot de passe une fois ma clé ajoutée.

10. **Service systemd prêt à l'emploi** : crée `/etc/systemd/system/atelier.service` (désactivé pour l'instant) qui lancera `/opt/atelier/bin/bambud --addr 0.0.0.0:7878` sous l'utilisateur `atelier`, avec `BAMBU_DATA_DIR=/var/lib/atelier`, redémarrage automatique, durcissement (`NoNewPrivileges`, `ProtectSystem=strict`, `ReadWritePaths=/var/lib/atelier /var/log/atelier`, `PrivateTmp`). Un fichier `/etc/atelier/atelier.env` (droits 600) contiendra plus tard le jeton d'API ; crée-le vide avec un commentaire.

11. **Mise en veille et alimentation** : désactive la mise en veille automatique, la suspension et l'extinction de l'écran qui couperait le réseau (paramètres GNOME de Zorin et `systemctl mask sleep.target suspend.target hibernate.target hybrid-sleep.target`) — demande-moi avant. Vérifie dans le BIOS/UEFI si un redémarrage automatique après coupure de courant existe (dis-moi juste comment faire).

12. **Sauvegarde** : liste les disques ou supports externes disponibles et propose une stratégie de sauvegarde de `/var/lib/atelier` (par ex. `restic` ou `rsync` quotidien vers un disque externe ou un NAS), sans la mettre en place sans mon accord.

13. **Mises à jour de sécurité** : active `unattended-upgrades` pour les mises à jour de sécurité uniquement.

14. **Bilan** : termine `RAPPORT.md` avec : inventaire, ce qui est installé (versions), résultat du test de slicing Bambu Studio headless (OK / KO + détails), ce qui reste à faire, et les valeurs à me transmettre (IP, nom d'hôte, architecture, RAM, espace disque, version d'Ubuntu de base, chemin de l'AppImage). Affiche ce bilan à la fin.

---
