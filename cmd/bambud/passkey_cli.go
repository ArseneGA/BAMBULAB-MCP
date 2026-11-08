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

// passkeyMain : `bambud passkey` (état et liste), `--revoke ID`, `--require=on|off`. Comme `bambud enroll` et
// `bambud launch-code`, ces commandes s'exécutent sur le poste de l'humain et écrivent dans la base. L'enregistrement
// d'une passkey ne se fait PAS ici : il exige un navigateur (page /setup/passkeys). `--require=off` est le secours si
// l'humain a perdu toutes ses passkeys alors que « exiger une passkey » est actif.
func passkeyMain(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("bambud passkey", flag.ContinueOnError)
	fs.SetOutput(errw)
	dataDir := fs.String("data-dir", envOr("BAMBU_DATA_DIR", "./data"), "dossier de données du service (env BAMBU_DATA_DIR)")
	addr := fs.String("addr", envOr("BAMBU_ADDR", "127.0.0.1:7878"), "adresse d'écoute du service (pour afficher le lien)")
	public := fs.String("public-url", os.Getenv("BAMBU_PUBLIC_URL"), "URL publique du service (env BAMBU_PUBLIC_URL)")
	list := fs.Bool("list", false, "liste les passkeys (c'est aussi le défaut)")
	revoke := fs.String("revoke", "", "révoque la passkey d'identifiant ID (préfixe accepté si non ambigu)")
	require := fs.String("require", "", "on : exiger une passkey (le code de lancement et le bouton simple deviennent indisponibles) ; off : la passkey redevient facultative")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := validatePublicURL(*public); err != nil {
		fmt.Fprintln(errw, "erreur :", err)
		return 2
	}
	if *require != "" && *require != "on" && *require != "off" {
		fmt.Fprintln(errw, "erreur : --require attend on ou off")
		return 2
	}
	st, err := openStoreForCLI(*dataDir)
	if err != nil {
		fmt.Fprintln(errw, "erreur :", err)
		return 1
	}
	defer st.Close()
	pk := humanauth.PasskeysFor(st)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	base := publicBase(*public, *addr)
	if *revoke != "" {
		id, err := pk.Revoke(ctx, *revoke, "human:cli")
		if err != nil {
			fmt.Fprintln(errw, "erreur :", err)
			return 1
		}
		fmt.Fprintf(out, "Passkey %s révoquée.\n", id)
	}
	if *require != "" {
		if err := pk.SetRequired(ctx, *require == "on", "human:cli"); err != nil {
			fmt.Fprintln(errw, "erreur :", err)
			return 1
		}
		if *require == "on" {
			fmt.Fprintln(out, "Passkey exigée : le bouton simple et le code de lancement sont désormais indisponibles.")
		} else {
			fmt.Fprintln(out, "Passkey facultative de nouveau : le bouton simple et le code de lancement refonctionnent.")
		}
	}
	_ = list
	keys, err := pk.List(ctx)
	if err != nil {
		fmt.Fprintln(errw, "erreur :", err)
		return 1
	}
	active := 0
	for _, k := range keys {
		if !k.Revoked {
			active++
		}
	}
	state := "facultative (défaut)"
	if pk.Required(ctx) {
		state = "EXIGÉE"
	}
	fmt.Fprintf(out, "Passkey : %s ; %d active(s) sur %d enregistrée(s).\n", state, active, len(keys))
	for _, k := range keys {
		status := "active"
		if k.Revoked {
			status = "révoquée"
		}
		last := "jamais utilisée"
		if !k.LastUsedAt.IsZero() {
			last = "dernier usage " + k.LastUsedAt.Local().Format("2006-01-02 15:04")
		}
		fmt.Fprintf(out, "  %s  %-20s %s  site %s, %s\n", k.ID, k.Label, status, k.RPID, last)
	}
	fmt.Fprintf(out, "Enregistrer ou gérer les passkeys : %s/setup/passkeys (navigateur enregistré, en https ou sur localhost).\n", base)
	return 0
}
