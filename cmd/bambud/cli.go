package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/agentlink"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// Commandes humaines (docs/SECURITY.md) : elles s'exécutent sur le TERMINAL de l'humain et écrivent
// directement dans la base partagée avec le service (SQLite WAL, verrou d'écriture court) ; le service
// relit les tables à chaque usage. Rien ne passe par MCP ni par l'API JSON : le modèle ne peut ni voir
// ni provoquer l'affichage d'un code d'enregistrement ou d'un jeton d'appairage.

// publicBase déduit l'URL que l'humain ouvre : --public-url s'il est fourni, sinon l'adresse d'écoute
// (un hôte non spécifié 0.0.0.0 / [::] devient 127.0.0.1 : jamais « http://[::]:7878 »).
func publicBase(publicURL, addr string) string {
	if l := splitPublicURLs(publicURL); len(l) > 0 {
		return l[0] // la première URL sert aux liens générés
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://127.0.0.1:7878"
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// validatePublicURL refuse une URL publique inutilisable (schéma, hôte, chemin).
func validatePublicURL(list string) error {
	for _, p := range splitPublicURLs(list) {
		if err := validateOnePublicURL(p); err != nil {
			return err
		}
	}
	if strings.TrimSpace(list) != "" && len(splitPublicURLs(list)) == 0 {
		return fmt.Errorf("--public-url %q invalide", list)
	}
	return nil
}

// splitPublicURLs découpe une liste d'URL publiques séparées par des virgules (sans "/" final ni vides).
// La première sert aux liens générés ; toutes autorisent leur nom d'hôte.
func splitPublicURLs(list string) []string {
	var out []string
	for _, p := range strings.Split(list, ",") {
		if p = strings.TrimRight(strings.TrimSpace(p), "/"); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func validateOnePublicURL(p string) error {
	u, err := url.Parse(p)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return fmt.Errorf("--public-url %q invalide : attendu http(s)://hôte[:port]", p)
	}
	if h := u.Hostname(); h == "0.0.0.0" || h == "::" {
		return fmt.Errorf("--public-url %q : adresse non spécifiée", p)
	}
	if u.Path != "" && u.Path != "/" || u.RawQuery != "" {
		return fmt.Errorf("--public-url %q : pas de chemin ni de paramètres", p)
	}
	return nil
}

func openStoreForCLI(dataDir string) (*store.Store, error) {
	if _, err := os.Stat(dataDir); err != nil {
		return nil, fmt.Errorf("dossier de données %q introuvable (--data-dir) : %w", dataDir, err)
	}
	return store.Open(filepath.Join(dataDir, "bambu.db"), store.Options{})
}

// enrollMain : `bambud enroll` (nouveau code), `--list`, `--revoke ID`, `--revoke-all`.
func enrollMain(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("bambud enroll", flag.ContinueOnError)
	fs.SetOutput(errw)
	dataDir := fs.String("data-dir", envOr("BAMBU_DATA_DIR", "./data"), "dossier de données du service (env BAMBU_DATA_DIR)")
	addr := fs.String("addr", envOr("BAMBU_ADDR", "127.0.0.1:7878"), "adresse d'écoute du service (pour afficher le lien)")
	public := fs.String("public-url", os.Getenv("BAMBU_PUBLIC_URL"), "URL publique du service (env BAMBU_PUBLIC_URL)")
	list := fs.Bool("list", false, "liste les navigateurs enregistrés")
	revoke := fs.String("revoke", "", "révoque le navigateur d'identifiant donné (préfixe accepté)")
	revokeAll := fs.Bool("revoke-all", false, "révoque tous les navigateurs enregistrés")
	if err := fs.Parse(args); err != nil {
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
	svc := humanauth.New(st, humanauth.Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	switch {
	case *list:
		ss, err := svc.List(ctx)
		if err != nil {
			fmt.Fprintln(errw, "erreur :", err)
			return 1
		}
		if len(ss) == 0 {
			fmt.Fprintln(out, "Aucun navigateur enregistré.")
		}
		for _, s := range ss {
			state := "actif"
			switch {
			case s.Revoked:
				state = "révoqué"
			case !time.Now().Before(s.ExpiresAt):
				state = "expiré"
			}
			fmt.Fprintf(out, "%s  %-8s  créé %s  vu %s  expire %s  %s\n", s.ID, state, s.CreatedAt.Local().Format("2006-01-02 15:04"),
				s.LastSeenAt.Local().Format("2006-01-02 15:04"), s.ExpiresAt.Local().Format("2006-01-02"), s.Label)
		}
		return 0
	case *revokeAll:
		n, err := svc.RevokeAll(ctx)
		if err != nil {
			fmt.Fprintln(errw, "erreur :", err)
			return 1
		}
		fmt.Fprintf(out, "%d navigateur(s) révoqué(s).\n", n)
		return 0
	case *revoke != "":
		if _, err := svc.Revoke(ctx, *revoke); err != nil {
			fmt.Fprintln(errw, "erreur :", err)
			return 1
		}
		fmt.Fprintln(out, "Navigateur révoqué.")
		return 0
	}
	code, exp, err := svc.NewEnrollCode(ctx)
	if err != nil {
		fmt.Fprintln(errw, "erreur :", err)
		return 1
	}
	base := publicBase(*public, *addr)
	fmt.Fprintf(out, "Code d'enregistrement : %s\n", humanauth.FormatCode(code))
	fmt.Fprintf(out, "Usage unique, valable jusqu'à %s.\n", exp.Local().Format("15:04:05"))
	fmt.Fprintf(out, "Dans le navigateur à enregistrer, ouvre %s/enroll et saisis ce code.\n", base)
	fmt.Fprintln(out, "Ne le communique à personne, et surtout pas dans la conversation avec Claude.")
	return 0
}

// agentMain : `bambud agent pair` crée un jeton d'appairage à usage unique (affiché sur ce terminal).
func agentMain(args []string, out, errw io.Writer) int {
	if len(args) == 0 || args[0] != "pair" {
		fmt.Fprintln(errw, "usage : bambud agent pair [--data-dir D] [--public-url U]")
		return 2
	}
	fs := flag.NewFlagSet("bambud agent pair", flag.ContinueOnError)
	fs.SetOutput(errw)
	dataDir := fs.String("data-dir", envOr("BAMBU_DATA_DIR", "./data"), "dossier de données du service")
	addr := fs.String("addr", envOr("BAMBU_ADDR", "127.0.0.1:7878"), "adresse d'écoute du service")
	public := fs.String("public-url", os.Getenv("BAMBU_PUBLIC_URL"), "URL publique du service")
	if err := fs.Parse(args[1:]); err != nil {
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
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tok, exp, err := agentlink.CreatePairing(ctx, st, agentlink.DefaultPairingTTL)
	if err != nil {
		fmt.Fprintln(errw, "erreur :", err)
		return 1
	}
	fmt.Fprintf(out, "Jeton d'appairage (usage unique, valable jusqu'à %s) : %s\n", exp.Local().Format("15:04:05"), tok)
	fmt.Fprintf(out, "Sur le poste Windows : bambu-agent pair --server %s --token %s\n", publicBase(*public, *addr), tok)
	return 0
}
