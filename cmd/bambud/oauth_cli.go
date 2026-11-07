package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/oauth"
)

// oauthMain : `bambud oauth --list`, `--revoke ID`, `--revoke-all`. Gère les autorisations OAuth accordées aux
// connecteurs distants (Claude) ; même canal que `bambud enroll` : le terminal de l'humain, la base partagée.
func oauthMain(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("bambud oauth", flag.ContinueOnError)
	fs.SetOutput(errw)
	dataDir := fs.String("data-dir", envOr("BAMBU_DATA_DIR", "./data"), "dossier de données du service (env BAMBU_DATA_DIR)")
	list := fs.Bool("list", false, "liste les autorisations accordées")
	revoke := fs.String("revoke", "", "révoque l'autorisation d'identifiant donné (préfixe accepté) : ses jetons cessent de valoir")
	revokeAll := fs.Bool("revoke-all", false, "révoque toutes les autorisations OAuth")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !*list && *revoke == "" && !*revokeAll {
		fmt.Fprintln(errw, "usage : bambud oauth --list | --revoke ID | --revoke-all")
		return 2
	}
	st, err := openStoreForCLI(*dataDir)
	if err != nil {
		fmt.Fprintln(errw, "erreur :", err)
		return 1
	}
	defer st.Close()
	svc := oauth.New(st, oauth.Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	switch {
	case *list:
		gs, err := svc.List(ctx)
		if err != nil {
			fmt.Fprintln(errw, "erreur :", err)
			return 1
		}
		if len(gs) == 0 {
			fmt.Fprintln(out, "Aucune autorisation OAuth.")
		}
		for _, g := range gs {
			state := "actif"
			switch {
			case g.Revoked:
				state = "révoqué"
			case !time.Now().Before(g.ExpiresAt):
				state = "expiré"
			}
			fmt.Fprintf(out, "%s  %-8s  créé %s  utilisé %s  rafraîchissement jusqu'au %s  %s\n", g.ID, state, g.CreatedAt.Local().Format("2006-01-02 15:04"),
				g.LastUsedAt.Local().Format("2006-01-02 15:04"), g.ExpiresAt.Local().Format("2006-01-02"), g.ClientName)
		}
		return 0
	case *revokeAll:
		n, err := svc.RevokeAll(ctx)
		if err != nil {
			fmt.Fprintln(errw, "erreur :", err)
			return 1
		}
		fmt.Fprintf(out, "%d autorisation(s) révoquée(s).\n", n)
		return 0
	}
	if err := svc.Revoke(ctx, *revoke); err != nil {
		fmt.Fprintln(errw, "erreur :", err)
		return 1
	}
	fmt.Fprintln(out, "Autorisation révoquée.")
	return 0
}
