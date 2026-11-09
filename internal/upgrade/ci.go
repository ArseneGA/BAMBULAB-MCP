package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// CIState : verdict de la CI GitHub pour un commit.
type CIState string

const (
	CIGreen   CIState = "green"   // tous les contrôles terminés et réussis
	CIRed     CIState = "red"     // au moins un contrôle en échec (ou annulé)
	CIPending CIState = "pending" // contrôles en cours, ou pas encore créés
	CIUnknown CIState = "unknown" // GitHub injoignable, limite de débit, dépôt privé... : on attend
)

// CIResult est le verdict et sa justification lisible (journal, notification).
type CIResult struct {
	State  CIState
	Detail string
}

// Contrôles terminés qui comptent comme réussis ; tout autre résultat terminé est un échec.
var okConclusions = map[string]bool{"success": true, "neutral": true, "skipped": true}

type checkRun struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

// CIChecker interroge l'API publique de GitHub (check-runs et statut combiné du commit). Sans jeton, la limite de
// débit est de 60 requêtes par heure et par adresse : suffisant, car l'API n'est appelée que lorsqu'un nouveau commit
// existe. Un jeton facultatif (fichier 0600) relève la limite ; il n'est envoyé qu'à BaseURL et jamais journalisé.
type CIChecker struct {
	BaseURL        string // https://api.github.com
	Owner, Repo    string
	Token          string
	RequiredChecks []string // vide : tous les contrôles du commit
	HTTP           *http.Client
}

// ParseRepoURL extrait propriétaire et dépôt de https://github.com/Owner/Repo(.git).
func ParseRepoURL(repo string) (owner, name string, err error) {
	u, err := url.Parse(strings.TrimSpace(repo))
	if err != nil || u.Host == "" {
		return "", "", fmt.Errorf("URL de dépôt %q invalide", repo)
	}
	parts := strings.Split(strings.Trim(strings.TrimSuffix(u.Path, ".git"), "/"), "/")
	if len(parts) < 2 || parts[len(parts)-2] == "" || parts[len(parts)-1] == "" {
		return "", "", fmt.Errorf("URL de dépôt %q : attendu https://hôte/propriétaire/dépôt", repo)
	}
	return parts[len(parts)-2], parts[len(parts)-1], nil
}

func (c *CIChecker) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

type apiError struct {
	status      int
	rateLimited bool
	msg         string
}

func (e *apiError) Error() string { return e.msg }

func (c *CIChecker) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "atelier-bambu-updater")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return &apiError{msg: "GitHub injoignable : " + scrub(err.Error(), c.Token)}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	switch {
	case resp.StatusCode == http.StatusOK:
		if err := json.Unmarshal(body, out); err != nil {
			return &apiError{status: 200, msg: "réponse GitHub illisible : " + err.Error()}
		}
		return nil
	case resp.StatusCode == http.StatusTooManyRequests || (resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0"):
		return &apiError{status: resp.StatusCode, rateLimited: true,
			msg: "limite de débit de l'API GitHub atteinte (un jeton en lecture seule dans " + ConfFileName + " / github-token la relève)"}
	default:
		return &apiError{status: resp.StatusCode, msg: fmt.Sprintf("GitHub a répondu HTTP %d pour %s", resp.StatusCode, path)}
	}
}

func scrub(s, secret string) string {
	if secret != "" {
		s = strings.ReplaceAll(s, secret, "***")
	}
	return s
}

// Status renvoie le verdict de la CI pour sha. Il ne renvoie jamais d'erreur : un échec d'accès est « unknown ».
func (c *CIChecker) Status(ctx context.Context, sha string) CIResult {
	base := "/repos/" + url.PathEscape(c.Owner) + "/" + url.PathEscape(c.Repo) + "/commits/" + url.PathEscape(sha)

	var runs []checkRun
	for page := 1; page <= 5; page++ {
		var resp struct {
			Total int        `json:"total_count"`
			Runs  []checkRun `json:"check_runs"`
		}
		if err := c.get(ctx, fmt.Sprintf("%s/check-runs?per_page=100&page=%d", base, page), &resp); err != nil {
			return CIResult{CIUnknown, err.Error()}
		}
		runs = append(runs, resp.Runs...)
		if len(runs) >= resp.Total || len(resp.Runs) == 0 {
			break
		}
	}
	var combined struct {
		State string `json:"state"`
		Total int    `json:"total_count"`
	}
	if err := c.get(ctx, base+"/status", &combined); err != nil {
		return CIResult{CIUnknown, err.Error()}
	}
	return Evaluate(runs, combined.State, combined.Total, c.RequiredChecks)
}

// Evaluate décide du verdict à partir des contrôles. Fonction pure (testée sans réseau).
func Evaluate(runs []checkRun, combinedState string, combinedTotal int, required []string) CIResult {
	considered := runs
	var missing []string
	if len(required) > 0 {
		byName := map[string]checkRun{}
		for _, r := range runs {
			byName[r.Name] = r
		}
		considered = nil
		for _, n := range required {
			if r, ok := byName[n]; ok {
				considered = append(considered, r)
			} else {
				missing = append(missing, n)
			}
		}
	}
	var red, pending []string
	for _, r := range considered {
		switch {
		case r.Status != "completed":
			pending = append(pending, r.Name)
		case !okConclusions[r.Conclusion]:
			red = append(red, r.Name+" ("+r.Conclusion+")")
		}
	}
	// Anciens « statuts » de commit (hors Actions) : un échec compte, un statut en cours aussi.
	if combinedTotal > 0 && len(required) == 0 {
		switch combinedState {
		case "failure", "error":
			red = append(red, "statuts du commit ("+combinedState+")")
		case "pending":
			pending = append(pending, "statuts du commit")
		}
	}
	sort.Strings(red)
	sort.Strings(pending)
	switch {
	case len(red) > 0:
		return CIResult{CIRed, "CI rouge : " + strings.Join(red, ", ")}
	case len(missing) > 0:
		return CIResult{CIPending, "contrôles requis pas encore vus : " + strings.Join(missing, ", ")}
	case len(pending) > 0:
		return CIResult{CIPending, "CI en cours : " + strings.Join(pending, ", ")}
	case len(considered) == 0 && combinedTotal == 0:
		return CIResult{CIPending, "aucun contrôle de CI enregistré pour ce commit (pas encore démarrée ?)"}
	}
	return CIResult{CIGreen, fmt.Sprintf("CI verte (%d contrôle(s))", len(considered)+combinedTotal)}
}
