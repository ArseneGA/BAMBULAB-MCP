package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/backup"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// Commandes d'exploitation (jalon 9) : sauvegarde, restauration, vérification. Elles s'exécutent sur le
// terminal de l'humain / le timer systemd ; aucune n'est exposée par HTTP ou MCP.

func cliContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func humanBytes(n int64) string {
	const k = 1024.0
	f := float64(n)
	switch {
	case f >= k*k*k:
		return fmt.Sprintf("%.2f Gio", f/(k*k*k))
	case f >= k*k:
		return fmt.Sprintf("%.1f Mio", f/(k*k))
	case f >= k:
		return fmt.Sprintf("%.1f Kio", f/k)
	}
	return fmt.Sprintf("%d o", n)
}

// backupMain : `bambud backup --out F.tar.gz` — copie cohérente sans arrêter le service.
func backupMain(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("bambud backup", flag.ContinueOnError)
	fs.SetOutput(errw)
	dataDir := fs.String("data-dir", envOr("BAMBU_DATA_DIR", "./data"), "dossier de données du service (env BAMBU_DATA_DIR)")
	dst := fs.String("out", "", "archive à écrire (.tar.gz) ; si c'est un dossier existant, un nom daté y est créé")
	noSecrets := fs.Bool("exclude-secrets", false, "n'inclut pas le coffre des codes d'accès ni les clés")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *dst == "" {
		fmt.Fprintln(errw, "erreur : --out est requis (ex. --out /srv/sauvegardes/atelier.tar.gz)")
		return 2
	}
	target := *dst
	if st, err := os.Stat(target); err == nil && st.IsDir() {
		target = filepath.Join(target, "atelier-"+time.Now().UTC().Format("20060102T150405Z")+".tar.gz")
	}
	ctx, stop := cliContext()
	defer stop()
	m, err := backup.Create(ctx, *dataDir, target, backup.CreateOptions{ExcludeSecrets: *noSecrets, Version: core.Version})
	if err != nil {
		fmt.Fprintln(errw, "erreur :", err)
		return 1
	}
	var total int64
	for _, f := range m.Files {
		total += f.Size
	}
	fmt.Fprintf(out, "Sauvegarde écrite : %s\n", target)
	fmt.Fprintf(out, "  base v%d, %d blob(s), %d fichier(s), %s de contenu, secrets %s\n", m.SchemaVersion, m.BlobCount, len(m.Files), humanBytes(total),
		map[bool]string{true: "inclus (chiffrés)", false: "exclus"}[m.IncludesSecrets])
	for _, w := range m.Warnings {
		fmt.Fprintln(out, "  attention :", w)
	}
	fmt.Fprintf(out, "  empreinte de l'archive : %s.sha256\n", target)
	if m.IncludesSecrets {
		fmt.Fprintln(out, "  L'archive contient la clé du coffre et le coffre des codes d'accès : la ranger sur un support à accès restreint.")
	}
	return 0
}

// restoreMain : `bambud restore --from F --data-dir D [--force]`.
func restoreMain(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("bambud restore", flag.ContinueOnError)
	fs.SetOutput(errw)
	dataDir := fs.String("data-dir", envOr("BAMBU_DATA_DIR", ""), "dossier de données à remplir (env BAMBU_DATA_DIR) ; requis")
	from := fs.String("from", "", "archive à restaurer (.tar.gz)")
	force := fs.Bool("force", false, "accepte un dossier non vide : son contenu est conservé dans .pre-restore-<date>/")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *from == "" || *dataDir == "" {
		fmt.Fprintln(errw, "erreur : --from et --data-dir sont requis")
		return 2
	}
	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		fmt.Fprintln(errw, "erreur :", err)
		return 1
	}
	// Jamais sur les données d'un service en marche : on prend le même verrou que lui.
	release, err := core.LockDataDir(*dataDir)
	if err != nil {
		fmt.Fprintln(errw, "erreur :", err)
		return 1
	}
	defer release()
	ctx, stop := cliContext()
	defer stop()
	m, err := backup.Restore(ctx, *from, *dataDir, backup.RestoreOptions{Force: *force, MaxSchema: store.LatestSchemaVersion()})
	if err != nil {
		fmt.Fprintln(errw, "erreur :", err)
		return 1
	}
	fmt.Fprintf(out, "Restauration terminée dans %s (sauvegarde du %s, base v%d, %d blob(s)).\n", *dataDir, m.CreatedAt, m.SchemaVersion, m.BlobCount)
	if !m.IncludesSecrets {
		fmt.Fprintln(out, "  Cette archive n'avait pas les secrets : les codes d'accès des imprimantes sont à ressaisir (page /setup).")
	}
	fmt.Fprintln(out, "  Vérification conseillée : bambud verify --data-dir", *dataDir)
	return 0
}

// verifyMain : `bambud verify [--data-dir D | --archive F]`.
func verifyMain(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("bambud verify", flag.ContinueOnError)
	fs.SetOutput(errw)
	dataDir := fs.String("data-dir", envOr("BAMBU_DATA_DIR", "./data"), "dossier de données à contrôler (env BAMBU_DATA_DIR)")
	archive := fs.String("archive", "", "contrôle une archive de sauvegarde au lieu du dossier de données")
	quick := fs.Bool("quick", false, "dossier de données : ne relit pas les blobs (présence seulement)")
	asJSON := fs.Bool("json", false, "sortie JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ctx, stop := cliContext()
	defer stop()
	var rep *backup.VerifyReport
	var err error
	if *archive != "" {
		rep, _, err = backup.VerifyArchive(ctx, *archive)
	} else {
		rep, err = backup.VerifyDataDir(ctx, *dataDir, *quick)
	}
	if err != nil {
		fmt.Fprintln(errw, "erreur :", err)
		return 1
	}
	if *asJSON {
		b, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Fprintln(out, string(b))
	} else {
		what := "dossier de données"
		if rep.Kind == "archive" {
			what = "archive"
		}
		fmt.Fprintf(out, "Contrôle du %s : base v%d, %d blob(s) (%s), %d référence(s) de blob.\n", what, rep.SchemaVersion,
			rep.Blobs, humanBytes(rep.BlobBytes), rep.References)
		for _, w := range rep.Warnings {
			fmt.Fprintln(out, "  attention :", w)
		}
		for _, p := range rep.Problems {
			fmt.Fprintln(out, "  PROBLÈME :", p)
		}
		if rep.OK() {
			fmt.Fprintln(out, "OK : aucune anomalie.")
		}
	}
	if !rep.OK() {
		return 1
	}
	return 0
}

// parseWrapper découpe une commande préfixe ("xvfb-run -a"). "none" ou "-" la désactive.
func parseWrapper(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "none") || s == "-" {
		return nil
	}
	return strings.Fields(s)
}
