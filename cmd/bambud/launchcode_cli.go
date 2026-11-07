package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth"
)

// launchCodeMain : `bambud launch-code` (état), `--unlock` (lève le verrouillage, garde le code), `--reset`
// (supprime le code ET le verrou ; le nouveau code se définit sur /setup/launch-code). Ces commandes s'exécutent
// sur le poste de l'humain : elles écrivent dans la base comme `bambud enroll`. Le code lui-même n'est jamais saisi
// ni affiché ici (il se définit sur la page du navigateur enregistré).
func launchCodeMain(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("bambud launch-code", flag.ContinueOnError)
	fs.SetOutput(errw)
	dataDir := fs.String("data-dir", envOr("BAMBU_DATA_DIR", "./data"), "dossier de données du service (env BAMBU_DATA_DIR)")
	addr := fs.String("addr", envOr("BAMBU_ADDR", "127.0.0.1:7878"), "adresse d'écoute du service (pour afficher le lien)")
	public := fs.String("public-url", os.Getenv("BAMBU_PUBLIC_URL"), "URL publique du service (env BAMBU_PUBLIC_URL)")
	reset := fs.Bool("reset", false, "supprime le code de lancement et lève le verrouillage (le nouveau code se définit sur /setup/launch-code)")
	unlock := fs.Bool("unlock", false, "lève le verrouillage après 3 échecs, en gardant le code actuel")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *reset && *unlock {
		fmt.Fprintln(errw, "erreur : --reset et --unlock s'excluent")
		return 2
	}
	if err := validatePublicURL(*public); err != nil {
		fmt.Fprintln(errw, "erreur :", err)
		return 2
	}
	st, err := openStoreForCLI(*dataDir)
	if err != nil {
		fmt.Fprintln(errw, "erreur :", err)
		return 1
	}
	defer st.Close()
	lc := humanauth.LaunchCodesFor(st)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	base := publicBase(*public, *addr)
	switch {
	case *reset:
		if err := lc.Revoke(ctx, "human:cli"); err != nil {
			fmt.Fprintln(errw, "erreur :", err)
			return 1
		}
		fmt.Fprintln(out, "Code de lancement supprimé (et verrouillage levé). La vue de Claude n'affiche plus que le lien du navigateur.")
		fmt.Fprintf(out, "Pour en définir un nouveau, ouvre %s/setup/launch-code dans un navigateur enregistré.\n", base)
		return 0
	case *unlock:
		if err := lc.Unlock(ctx, "human:cli"); err != nil {
			fmt.Fprintln(errw, "erreur :", err)
			return 1
		}
		fmt.Fprintln(out, "Verrouillage levé : le code actuel est de nouveau utilisable.")
		return 0
	}
	s, err := lc.Status(ctx)
	if err != nil {
		fmt.Fprintln(errw, "erreur :", err)
		return 1
	}
	switch {
	case !s.Set:
		fmt.Fprintln(out, "Aucun code de lancement défini.")
	case s.Locked:
		fmt.Fprintln(out, "Code de lancement défini, VERROUILLÉ (3 échecs). Déverrouiller : bambud launch-code --unlock")
	default:
		fmt.Fprintf(out, "Code de lancement défini, actif (%d essai(s) avant verrouillage).\n", s.AttemptsLeft)
	}
	fmt.Fprintf(out, "Définir ou changer le code : %s/setup/launch-code (navigateur enregistré). Ne le tape jamais dans la conversation avec Claude.\n", base)
	return 0
}
