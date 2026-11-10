package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// `bambud skip-objects` : geste HUMAIN, sur le terminal du serveur, pour ne plus imprimer un objet raté d'une plaque
// pendant qu'elle s'imprime (étape SK0 de docs/specs/SK-ignorer-un-objet.md ; mode d'emploi : docs/IGNORER-OBJET.md).
//
//	bambud skip-objects --job <print_job_id> --list              objets de l'impression (aucun envoi)
//	bambud skip-objects --job <print_job_id> --ids 130,174       ignorer ces objets (confirmation interactive obligatoire)
//
// La commande n'ouvre NI la base NI l'imprimante : elle parle au service en marche par ses routes locales et réservées
// (/api/system/skip-objects/*, loopback seulement, jeton d'API si configuré ; voir internal/httpapi/skip_local.go), qui
// tient déjà l'unique session MQTT de l'imprimante, l'audit et le journal. Il n'y a AUCUNE migration de base ici.
// Pas d'option --yes : la confirmation tapée au clavier est obligatoire, et refusée si l'entrée n'est pas un terminal.

// skipEnv : dépendances de la sous-commande (substituables dans les tests).
type skipEnv struct {
	in     io.Reader
	isTTY  func() bool
	out    io.Writer
	errw   io.Writer
	client *http.Client
}

func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func skipObjectsMain(args []string, out, errw io.Writer) int {
	return runSkipObjects(args, skipEnv{in: os.Stdin, isTTY: stdinIsTerminal, out: out, errw: errw, client: &http.Client{Timeout: 5 * time.Minute}})
}

// skipBase : adresse du service pour la sous-commande. Hôte non spécifié ou loopback : 127.0.0.1 (les routes exigent une
// connexion directe depuis le loopback).
func skipBase(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("--addr %q invalide : attendu hôte:port", addr)
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); host == "" || host == "localhost" || (ip != nil && (ip.IsUnspecified() || ip.IsLoopback())) {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port), nil
}

func parseSkipIDs(s string) ([]int, error) {
	var ids []int
	seen := map[int]bool{}
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil, fmt.Errorf("--ids %q : identifiant vide", s)
		}
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("--ids : %q n'est pas un entier > 0", p)
		}
		if seen[n] {
			return nil, fmt.Errorf("--ids : identifiant %d en double", n)
		}
		seen[n] = true
		ids = append(ids, n)
	}
	if len(ids) > 64 {
		return nil, fmt.Errorf("--ids : %d identifiants (au plus 64)", len(ids))
	}
	return ids, nil
}

type skipObjJSON struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Skipped bool   `json:"skipped"`
}

// skipQuestion : la question de confirmation. Ex. « Ignorer 1 objet (« Pied #3 », id 174) ? Cette action est irréversible [o/N] ».
func skipQuestion(tg []skipObjJSON) string {
	var parts []string
	for _, o := range tg {
		parts = append(parts, fmt.Sprintf("« %s », id %d", o.Name, o.ID))
	}
	n := "1 objet"
	if len(tg) > 1 {
		n = fmt.Sprintf("%d objets", len(tg))
	}
	return fmt.Sprintf("Ignorer %s (%s) ? Cette action est irréversible [o/N] ", n, strings.Join(parts, " ; "))
}

func runSkipObjects(args []string, e skipEnv) int {
	for _, a := range args {
		switch strings.TrimLeft(strings.SplitN(a, "=", 2)[0], "-") {
		case "yes", "y", "assume-yes", "force", "no-confirm":
			fmt.Fprintln(e.errw, "erreur : il n'existe pas d'option "+a+" : la confirmation interactive est obligatoire (geste humain, au clavier)")
			return 2
		}
	}
	fs := flag.NewFlagSet("bambud skip-objects", flag.ContinueOnError)
	fs.SetOutput(e.errw)
	addr := fs.String("addr", envOr("BAMBU_ADDR", "127.0.0.1:7878"), "adresse d'écoute du service (env BAMBU_ADDR)")
	token := fs.String("api-token", os.Getenv("BAMBU_API_TOKEN"), "jeton d'API du service, s'il en exige un (env BAMBU_API_TOKEN)")
	job := fs.String("job", "", "identifiant de l'impression (print_job_id)")
	idsArg := fs.String("ids", "", "identifiants des objets à ignorer, séparés par des virgules (voir --list)")
	list := fs.Bool("list", false, "liste les objets de l'impression et dit si on peut en ignorer ; n'envoie rien")
	wait := fs.Duration("wait", 20*time.Second, "attente de la confirmation par l'imprimante (s_obj)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(e.errw, "erreur : argument inattendu %q\n", fs.Arg(0))
		return 2
	}
	if strings.TrimSpace(*job) == "" {
		fmt.Fprintln(e.errw, "usage : bambud skip-objects --job <print_job_id> (--list | --ids 130,174 [--wait 20s])")
		return 2
	}
	if *wait < time.Second || *wait > 2*time.Minute {
		fmt.Fprintln(e.errw, "erreur : --wait entre 1s et 2m")
		return 2
	}
	var ids []int
	if *list {
		if *idsArg != "" {
			fmt.Fprintln(e.errw, "erreur : --list n'envoie rien ; ne pas donner --ids")
			return 2
		}
	} else {
		if *idsArg == "" {
			fmt.Fprintln(e.errw, "erreur : --ids requis (ou --list pour voir les objets)")
			return 2
		}
		var err error
		if ids, err = parseSkipIDs(*idsArg); err != nil {
			fmt.Fprintln(e.errw, "erreur :", err)
			return 2
		}
		if !e.isTTY() {
			fmt.Fprintln(e.errw, "erreur : l'entrée standard n'est pas un terminal : la confirmation interactive est obligatoire, rien n'est envoyé")
			return 2
		}
	}
	base, err := skipBase(*addr)
	if err != nil {
		fmt.Fprintln(e.errw, "erreur :", err)
		return 2
	}
	ctx := context.Background()
	call := func(path string, in, out any) error {
		b, _ := json.Marshal(in)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/system/skip-objects/"+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		if *token != "" {
			req.Header.Set("Authorization", "Bearer "+*token)
		}
		resp, err := e.client.Do(req)
		if err != nil {
			return fmt.Errorf("service injoignable sur %s (est-il démarré ? bon --addr ?) : %w", base, err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if resp.StatusCode != http.StatusOK {
			var er struct {
				Error string `json:"error"`
			}
			if json.Unmarshal(body, &er) == nil && er.Error != "" {
				return fmt.Errorf("%s", er.Error)
			}
			return fmt.Errorf("réponse %d du service", resp.StatusCode)
		}
		return json.Unmarshal(body, out)
	}

	if *list {
		var v struct {
			PrintJobID   string        `json:"print_job_id"`
			File         string        `json:"file"`
			JobState     string        `json:"job_state"`
			PrinterState string        `json:"printer_state"`
			Objects      []skipObjJSON `json:"objects"`
			CanSkip      bool          `json:"can_skip"`
			Refusal      string        `json:"refusal"`
		}
		if err := call("list", map[string]any{"job": *job}, &v); err != nil {
			fmt.Fprintln(e.errw, "erreur :", err)
			return 1
		}
		fmt.Fprintf(e.out, "Impression %s « %s » : état %s, imprimante %s\n", v.PrintJobID, v.File, v.JobState, orDash(v.PrinterState))
		fmt.Fprintln(e.out, "Objets de la plaque (identifiants à donner à --ids) :")
		for _, o := range v.Objects {
			mark := ""
			if o.Skipped {
				mark = "   [déjà ignoré]"
			}
			fmt.Fprintf(e.out, "  id %-8d « %s »%s\n", o.ID, o.Name, mark)
		}
		if v.CanSkip {
			fmt.Fprintln(e.out, "On peut ignorer un objet maintenant.")
		} else {
			fmt.Fprintln(e.out, "On ne peut pas ignorer d'objet maintenant :", v.Refusal)
		}
		return 0
	}

	var prep struct {
		PrintJobID   string        `json:"print_job_id"`
		File         string        `json:"file"`
		Targets      []skipObjJSON `json:"targets"`
		Remaining    []skipObjJSON `json:"remaining"`
		ConfirmToken string        `json:"confirm_token"`
	}
	if err := call("prepare", map[string]any{"job": *job, "ids": ids}, &prep); err != nil {
		fmt.Fprintln(e.errw, "refusé :", err)
		return 1
	}
	fmt.Fprintf(e.out, "Impression %s « %s ».\n", prep.PrintJobID, prep.File)
	var rest []string
	for _, o := range prep.Remaining {
		rest = append(rest, fmt.Sprintf("« %s » (id %d)", o.Name, o.ID))
	}
	fmt.Fprintf(e.out, "Continueront d'être imprimés : %s.\n", strings.Join(rest, ", "))
	fmt.Fprintln(e.out, "Attention : la tour de purge continue ; un support partagé ou un objet collé à un voisin peut laisser un support orphelin.")
	fmt.Fprint(e.out, skipQuestion(prep.Targets))
	ans, _ := bufio.NewReader(e.in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(ans)) {
	case "o", "oui":
	default:
		fmt.Fprintln(e.out, "Annulé : rien n'a été envoyé.")
		return 1
	}
	var res struct {
		Status       string        `json:"status"`
		Skipped      []int         `json:"skipped_objects"`
		PrinterReply string        `json:"printer_reply"`
		Message      string        `json:"message"`
		Targets      []skipObjJSON `json:"targets"`
	}
	if err := call("execute", map[string]any{"confirm_token": prep.ConfirmToken, "wait_s": wait.Seconds()}, &res); err != nil {
		fmt.Fprintln(e.errw, "erreur :", err)
		return 1
	}
	fmt.Fprintf(e.out, "Statut : %s\n%s\n", res.Status, res.Message)
	fmt.Fprintf(e.out, "s_obj de l'imprimante : %v\n", res.Skipped)
	if res.Status == "verified" {
		return 0
	}
	return 3
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
