package printing_test

import (
	"context"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth/passkeytest"
)

var (
	pkOptionsRe = regexp.MustCompile(`data-pk-options="([^"]*)"`)
	pkNonceRe   = regexp.MustCompile(`<form method="post"(?: autocomplete="off")?(?: data-pk-mode="[a-z]+" data-pk-options="[^"]*")?><input type="hidden" name="nonce" value="([^"]+)"`)
)

// pkBrowser : l'humain devant son navigateur ENREGISTRÉ sur http://localhost:<port> (un contexte sécurisé : les
// passkeys exigent https ou localhost ; l'adresse IP 127.0.0.1 de httptest ne convient pas).
type pkBrowser struct {
	t      *testing.T
	r      *rig
	base   string
	origin string
	cl     *http.Client
}

func newPKBrowser(t *testing.T, r *rig) *pkBrowser {
	t.Helper()
	base := strings.Replace(r.web.URL, "127.0.0.1", "localhost", 1)
	code, _, err := humanauth.New(r.c.Store, humanauth.Options{}).NewEnrollCode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	cl := &http.Client{Jar: jar, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if _, err := browserGet(cl, base+"/enroll"); err != nil {
		t.Fatal(err)
	}
	st, body := browserPost(t, cl, base+"/enroll", url.Values{"code": {humanauth.FormatCode(code)}, "label": {"localhost"}})
	if st != 200 || !strings.Contains(body, "enregistré") {
		t.Fatalf("enregistrement du navigateur localhost : %d %s", st, body)
	}
	return &pkBrowser{t: t, r: r, base: base, origin: base, cl: cl}
}

// get ouvre une page : statut, corps, en-tête CSP.
func (b *pkBrowser) get(path string) (int, string, string) {
	b.t.Helper()
	res, err := browserGet(b.cl, b.base+path)
	if err != nil {
		b.t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(body), res.Header.Get("Content-Security-Policy")
}

func (b *pkBrowser) post(path string, f url.Values) (int, string) {
	b.t.Helper()
	return browserPost(b.t, b.cl, b.base+path, f)
}

// pageOptions extrait les options WebAuthn (JSON) et le nonce d'une page.
func pageOptions(t *testing.T, body string) (opts []byte, nonce string) {
	t.Helper()
	if m := pkOptionsRe.FindStringSubmatch(body); m != nil {
		opts = []byte(html.UnescapeString(m[1]))
	}
	if m := pkNonceRe.FindStringSubmatch(body); m != nil {
		nonce = m[1]
	}
	return opts, nonce
}

// register joue le parcours complet d'enregistrement sur /setup/passkeys.
func (b *pkBrowser) register(a *passkeytest.Authenticator, label string) {
	b.t.Helper()
	st, body, _ := b.get("/setup/passkeys")
	opts, nonce := pageOptions(b.t, body)
	if st != 200 || opts == nil || nonce == "" {
		b.t.Fatalf("page /setup/passkeys : %d %s", st, body)
	}
	cred, err := a.Create(opts, b.origin)
	if err != nil {
		b.t.Fatal(err)
	}
	st, body = b.post("/setup/passkeys", url.Values{"nonce": {nonce}, "action": {"register"}, "label": {label}, "credential": {string(cred)}, "method": {"passkey"}})
	if st != 200 || !strings.Contains(body, "enregistrée") {
		b.t.Fatalf("enregistrement : %d %s", st, body)
	}
}

// approve : ouvre /confirm/{id} (ou /stop/{id}), signe avec la passkey, soumet.
func (b *pkBrowser) approve(a *passkeytest.Authenticator, path string, plate bool) (int, string) {
	b.t.Helper()
	st, body, _ := b.get(path)
	opts, nonce := pageOptions(b.t, body)
	if st != 200 || opts == nil || nonce == "" {
		b.t.Fatalf("page %s : %d %s", path, st, body)
	}
	as, err := a.Get(opts, b.origin)
	if err != nil {
		b.t.Fatal(err)
	}
	f := url.Values{"nonce": {nonce}, "method": {"passkey"}, "assertion": {string(as)}}
	if plate {
		f.Set("plate_clear", "yes")
	}
	return b.post(path, f)
}

func TestPasskeyRegistersOnThePageAndApprovesLaunch(t *testing.T) {
	r := launchRig(t)
	b := newPKBrowser(t, r)
	a := passkeytest.Must(passkeytest.ES256)

	// 1. La page de gestion : script statique de même origine, aucun JavaScript en ligne, CSP au minimum.
	st, body, csp := b.get("/setup/passkeys")
	if st != 200 || !strings.Contains(body, `src="/setup/passkeys/passkey.js"`) {
		t.Fatalf("page : %d %s", st, body)
	}
	if !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-inline'") && strings.Contains(csp, "script-src 'self' 'unsafe") {
		t.Fatalf("CSP : %s", csp)
	}
	if regexp.MustCompile(`(?i)<script(?:\s[^>]*)?>[^<]`).MatchString(body) || strings.Contains(strings.ToLower(body), "onclick=") {
		t.Fatalf("JavaScript en ligne dans la page : %s", body)
	}
	b.register(a, "clé de test")

	// 2. /confirm : le bouton simple reste, la passkey s'y ajoute.
	job, cf := r.printReady("cle-depot-passkey-1")
	confPath := "/confirm/" + str(cf, "confirmation_id")
	st, body, csp = b.get(confPath)
	for _, want := range []string{"Lancer l'impression", "Approuver avec ma passkey", "data-pk-options="} {
		if st != 200 || !strings.Contains(body, want) {
			t.Fatalf("/confirm sans %q : %d %s", want, st, body)
		}
	}
	if !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "form-action 'self'") {
		t.Fatalf("CSP de /confirm : %s", csp)
	}
	if n := len(r.sim.StartCommands()); n != 0 {
		t.Fatalf("rien ne doit partir avant l'approbation : %d", n)
	}

	// 3. Sans la case « plateau vide », la passkey ne suffit pas.
	if st, out := b.approve(a, confPath, false); st == 200 || len(r.sim.StartCommands()) != 0 {
		t.Fatalf("approbation sans attestation du plateau : %d %s", st, out)
	}
	// 4. Avec : approuvé ET lancé, une seule commande (même chemin ApproveAndStart que le bouton).
	_, page, _ := b.get(confPath)
	opts, nonce := pageOptions(t, page)
	as, err := a.Get(opts, b.origin)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"nonce": {nonce}, "method": {"passkey"}, "assertion": {string(as)}, "plate_clear": {"yes"}}
	st, out := b.post(confPath, form)
	if st != 200 || !strings.Contains(out, "Impression lancée") {
		t.Fatalf("approbation par passkey : %d %s", st, out)
	}
	if n := len(r.sim.StartCommands()); n != 1 || r.printState(job) != "PRINTING" {
		t.Fatalf("lancement : %d commande(s), état %s", n, r.printState(job))
	}
	// 5. Rejeu du MÊME formulaire (même nonce, même assertion) : refusé, rien ne repart.
	if st, out := b.post(confPath, form); st == 200 || strings.Contains(out, "Impression lancée") {
		t.Fatalf("rejeu accepté : %d", st)
	}
	if n := len(r.sim.StartCommands()); n != 1 {
		t.Fatalf("après rejeu : %d commandes", n)
	}
}

func TestPasskeyChallengeOfAnotherConfirmationIsRefusedOnThePage(t *testing.T) {
	r := launchRig(t)
	b := newPKBrowser(t, r)
	a := passkeytest.Must(passkeytest.ES256)
	b.register(a, "k")
	_, cf1 := r.printReady("cle-depot-passkey-A")
	_, cf2 := r.printReady("cle-depot-passkey-B")
	p1, p2 := "/confirm/"+str(cf1, "confirmation_id"), "/confirm/"+str(cf2, "confirmation_id")
	_, body1, _ := b.get(p1)
	_, body2, _ := b.get(p2)
	opts1, _ := pageOptions(t, body1)
	_, nonce2 := pageOptions(t, body2)
	// Signature faite pour la page de la confirmation 1, soumise à la confirmation 2.
	as, err := a.Get(opts1, b.origin)
	if err != nil {
		t.Fatal(err)
	}
	st, out := b.post(p2, url.Values{"nonce": {nonce2}, "method": {"passkey"}, "assertion": {string(as)}, "plate_clear": {"yes"}})
	if st != 403 || !strings.Contains(out, "challenge") {
		t.Fatalf("challenge d'une autre confirmation : %d %s", st, out)
	}
	if n := len(r.sim.StartCommands()); n != 0 {
		t.Fatalf("%d commande(s) envoyée(s)", n)
	}
}

func TestPasskeyPagesNeedAnEnrolledBrowser(t *testing.T) {
	r := launchRig(t)
	b := newPKBrowser(t, r)
	a := passkeytest.Must(passkeytest.ES256)
	b.register(a, "k")
	_, cf := r.printReady("cle-depot-passkey-anon")
	confPath := "/confirm/" + str(cf, "confirmation_id")
	_, body, _ := b.get(confPath)
	opts, nonce := pageOptions(t, body)
	as, _ := a.Get(opts, b.origin)

	jar, _ := cookiejar.New(nil) // un navigateur NON enregistré (pas de cookie)
	anon := &pkBrowser{t: t, r: r, base: b.base, origin: b.origin, cl: &http.Client{Jar: jar, Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	for _, p := range []string{"/setup/passkeys", confPath} {
		res, err := browserGet(anon.cl, anon.base+p)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusSeeOther || !strings.HasPrefix(res.Header.Get("Location"), "/enroll") {
			t.Fatalf("GET %s sans cookie : %d %s", p, res.StatusCode, res.Header.Get("Location"))
		}
	}
	// POST sans cookie : approbation et enregistrement refusés, même avec une vraie signature et un vrai nonce.
	if st, _ := anon.post(confPath, url.Values{"nonce": {nonce}, "method": {"passkey"}, "assertion": {string(as)}, "plate_clear": {"yes"}}); st != 403 {
		t.Fatalf("approbation sans navigateur enregistré : %d", st)
	}
	if st, _ := anon.post("/setup/passkeys", url.Values{"nonce": {"x"}, "action": {"require_off"}}); st != 403 {
		t.Fatalf("gestion des passkeys sans navigateur enregistré : %d", st)
	}
	// POST d'un navigateur enregistré mais SANS en-têtes de navigation (un appel de script, pas un formulaire) : refusé.
	req, _ := http.NewRequest("POST", b.base+confPath, strings.NewReader(url.Values{"nonce": {nonce}, "method": {"passkey"}, "assertion": {string(as)}, "plate_clear": {"yes"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	res, err := b.cl.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("POST non navigation : %d", res.StatusCode)
	}
	// Aucune route /api n'approuve par passkey.
	for _, p := range []string{"/api/passkeys", "/api/setup/passkeys", "/api/confirmations/" + str(cf, "confirmation_id") + "/passkey"} {
		st, _, _ := b.get(p)
		if st == 200 {
			t.Fatalf("%s répond 200", p)
		}
	}
	if n := len(r.sim.StartCommands()); n != 0 {
		t.Fatalf("%d commande(s) envoyée(s)", n)
	}
}

func TestPasskeyRequiredMakesCodeAndButtonUnavailable(t *testing.T) {
	r := launchRig(t)
	r.humanSetLaunchCode(goodCode)
	b := newPKBrowser(t, r)
	a := passkeytest.Must(passkeytest.EdDSA)

	// Sans passkey enregistrée, « exiger » est refusé : plus aucune approbation ne serait possible.
	st, body, _ := b.get("/setup/passkeys")
	_, nonce := pageOptions(t, body)
	if st, out := b.post("/setup/passkeys", url.Values{"nonce": {nonce}, "action": {"require_on"}}); st != 400 || !strings.Contains(out, "aucune passkey") {
		t.Fatalf("exiger sans passkey : %d %s", st, out)
	}
	b.register(a, "clé EdDSA")
	_, body, _ = b.get("/setup/passkeys")
	_, nonce = pageOptions(t, body)
	if st, out := b.post("/setup/passkeys", url.Values{"nonce": {nonce}, "action": {"require_on"}}); st != 200 || !strings.Contains(out, "Passkey exigée") {
		t.Fatalf("exiger : %d %s", st, out)
	}

	job, cf := r.printReady("cle-depot-passkey-req")
	confID := str(cf, "confirmation_id")
	confPath := "/confirm/" + confID

	// Le code de lancement (outil app-only) n'approuve plus, sans brûler un essai ; la vue retombe sur le navigateur.
	m := r.call("approve_print_with_code", approveArgs(cf, goodCode, true))
	if m["approved"] != false || str(m, "problem") != "not_set" {
		t.Fatalf("code avec passkey exigée : %v", m)
	}
	if vs := r.call("get_launch_view_state", map[string]any{"confirmation_id": confID}); vs["code_set"] != false {
		t.Fatalf("état de la vue : %v", vs)
	}
	if ls, err := humanauth.LaunchCodesFor(r.c.Store).Status(context.Background()); err != nil || !ls.Set || ls.AttemptsLeft != 3 || ls.Locked {
		t.Fatalf("le code lui-même ne doit pas être touché ni brûlé : %+v %v", ls, err)
	}
	// Le bouton simple n'est plus proposé, et son POST est refusé.
	st, body, _ = b.get(confPath)
	if st != 200 || strings.Contains(body, "Lancer l'impression") || !strings.Contains(body, "Approuver avec ma passkey") {
		t.Fatalf("page avec passkey exigée : %d %s", st, body)
	}
	_, nonce = pageOptions(t, body)
	if st, out := b.post(confPath, url.Values{"nonce": {nonce}, "plate_clear": {"yes"}}); st != 403 && st != 410 || strings.Contains(out, "Impression lancée") {
		t.Fatalf("bouton simple avec passkey exigée : %d %s", st, out)
	}
	if n := len(r.sim.StartCommands()); n != 0 {
		t.Fatalf("%d commande(s) envoyée(s) sans passkey", n)
	}
	// La passkey, elle, approuve et lance.
	if st, out := b.approve(a, confPath, true); st != 200 || !strings.Contains(out, "Impression lancée") {
		t.Fatalf("approbation par passkey : %d %s", st, out)
	}
	if n := len(r.sim.StartCommands()); n != 1 {
		t.Fatalf("%d commandes", n)
	}

	// Arrêt : le code et le bouton simple sont refusés, la passkey arrête.
	c := r.call("cancel_print", map[string]any{"print_job_id": job})
	stopURL := str(c, "stop_url")
	stopID := stopURL[strings.Index(stopURL, "stp_"):]
	if m := r.call("approve_stop_with_code", map[string]any{"stop_id": stopID, "code": goodCode}); m["approved"] != false || str(m, "problem") != "not_set" {
		t.Fatalf("arrêt par code avec passkey exigée : %v", m)
	}
	st, body, _ = b.get("/stop/" + stopID)
	if st != 200 || strings.Contains(body, "Arrêter l'impression</button>") || !strings.Contains(body, "Arrêter avec ma passkey") {
		t.Fatalf("page d'arrêt : %d %s", st, body)
	}
	_, nonce = pageOptions(t, body)
	if st, _ := b.post("/stop/"+stopID, url.Values{"nonce": {nonce}}); st != 403 || r.printState(job) != "PRINTING" {
		t.Fatalf("arrêt par bouton simple avec passkey exigée : %d, état %s", st, r.printState(job))
	}
	if st, out := b.approve(a, "/stop/"+stopID, false); st != 200 || !strings.Contains(out, "arrêtée") {
		t.Fatalf("arrêt par passkey : %d %s", st, out)
	}
	if r.printState(job) != "CANCELLED" {
		t.Fatalf("état après arrêt : %s", r.printState(job))
	}

	// On ne révoque pas la dernière passkey tant qu'elle est exigée ; on peut ne plus l'exiger.
	_, body, _ = b.get("/setup/passkeys")
	_, nonce = pageOptions(t, body)
	var id string
	if m := regexp.MustCompile(`name="id" value="(pk_[0-9a-f]+)"`).FindStringSubmatch(body); m != nil {
		id = m[1]
	}
	if st, out := b.post("/setup/passkeys", url.Values{"nonce": {nonce}, "action": {"revoke"}, "id": {id}}); st != 400 || !strings.Contains(out, "dernière passkey") {
		t.Fatalf("révocation de la dernière passkey : %d %s", st, out)
	}
	_, body, _ = b.get("/setup/passkeys")
	_, nonce = pageOptions(t, body)
	if st, out := b.post("/setup/passkeys", url.Values{"nonce": {nonce}, "action": {"require_off"}}); st != 200 || !strings.Contains(out, "facultative") {
		t.Fatalf("ne plus exiger : %d %s", st, out)
	}
}

func TestPasskeyDefaultsLeaveEverythingUnchanged(t *testing.T) {
	r := launchRig(t)
	b := newPKBrowser(t, r)
	_, cf := r.printReady("cle-depot-passkey-defaut")
	confPath := "/confirm/" + str(cf, "confirmation_id")
	st, body, csp := b.get(confPath)
	if st != 200 || strings.Contains(body, "passkey") || strings.Contains(body, "<script") || strings.Contains(csp, "script-src") {
		t.Fatalf("sans passkey, la page doit rester celle d'avant : %d csp=%s\n%s", st, csp, body)
	}
	if !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "img-src 'self'") {
		t.Fatalf("CSP de /confirm modifiée : %s", csp)
	}
	// Le code de lancement et le bouton simple marchent comme avant.
	r.humanSetLaunchCode(goodCode)
	if m := r.call("approve_print_with_code", approveArgs(cf, goodCode, true)); m["approved"] != true || len(r.sim.StartCommands()) != 1 {
		t.Fatalf("code de lancement : %v", m)
	}
}

// Sur une adresse IP (127.0.0.1), une passkey n'est pas possible : la page le dit, /confirm n'affiche pas le bouton
// passkey et le bouton simple reste utilisable (passkey facultative).
func TestPasskeyUnavailableOnAnIPAddressKeepsTheButton(t *testing.T) {
	r := launchRig(t)
	b := newPKBrowser(t, r)
	b.register(passkeytest.Must(passkeytest.ES256), "k")
	_, cf := r.printReady("cle-depot-passkey-ip")
	confID := str(cf, "confirmation_id")
	// Même navigateur, mais par l'adresse IP : on s'y enregistre d'abord (cookie d'un autre hôte).
	ip := &pkBrowser{t: t, r: r, base: r.web.URL, origin: r.web.URL, cl: r.human}
	st, body, _ := ip.get("/setup/passkeys")
	if st != 200 || !strings.Contains(body, "adresse IP") {
		t.Fatalf("page par adresse IP : %d %s", st, body)
	}
	st, body, _ = ip.get("/confirm/" + confID)
	if st != 200 || strings.Contains(body, "Approuver avec ma passkey") || !strings.Contains(body, "Lancer l'impression") || !strings.Contains(body, "Passkey indisponible ici") {
		t.Fatalf("/confirm par adresse IP : %d %s", st, body)
	}
}

func TestPasskeyScriptIsStaticAndNotInline(t *testing.T) {
	r := launchRig(t)
	b := newPKBrowser(t, r)
	st, body, _ := b.get(humanauth.PasskeyScriptPath)
	if st != 200 || !strings.Contains(body, "navigator.credentials") || strings.Contains(body, "eval(") || strings.Contains(body, "innerHTML") {
		t.Fatalf("script : %d", st)
	}
	var v any
	if json.Unmarshal([]byte(body), &v) == nil {
		t.Fatal("le script ne doit pas être du JSON")
	}
}

// Les actions /action/{id} (reprise, chargement, nettoyage) respectent « exiger une passkey » : le code et le bouton
// simple sont refusés sans rien envoyer, la passkey exécute une seule commande.
func TestPasskeyRequiredAppliesToActions(t *testing.T) {
	r := launchRig(t)
	r.humanSetLaunchCode(goodCode)
	b := newPKBrowser(t, r)
	a := passkeytest.Must(passkeytest.EdDSA)
	b.register(a, "clé EdDSA")
	_, body, _ := b.get("/setup/passkeys")
	_, nonce := pageOptions(t, body)
	if st, out := b.post("/setup/passkeys", url.Values{"nonce": {nonce}, "action": {"require_on"}}); st != 200 {
		t.Fatalf("exiger : %d %s", st, out)
	}
	ld := r.call("load_filament", map[string]any{"printer_id": r.printerID})
	id := str(ld, "action_id")
	if m := r.call("validate_action_with_code", map[string]any{"action_id": id, "code": goodCode}); m["approved"] != false || str(m, "problem") != "not_set" {
		t.Fatalf("code avec passkey exigée : %v", m)
	}
	st, body, _ := b.get("/action/" + id)
	if st != 200 || strings.Contains(body, "Charger le filament</button>") || !strings.Contains(body, "Valider avec ma passkey") {
		t.Fatalf("page d'action : %d %s", st, body)
	}
	_, nonce = pageOptions(t, body)
	if st, _ := b.post("/action/"+id, url.Values{"nonce": {nonce}}); st != 403 {
		t.Fatalf("bouton simple avec passkey exigée : %d", st)
	}
	if n := r.sim.CommandCount("ams_change_filament"); n != 0 {
		t.Fatalf("%d commande(s) envoyée(s) sans passkey", n)
	}
	if st, out := b.approve(a, "/action/"+id, false); st != 200 {
		t.Fatalf("approbation par passkey : %d %s", st, out)
	}
	if n := r.sim.CommandCount("ams_change_filament"); n != 1 {
		t.Fatalf("%d commande(s), 1 attendue", n)
	}
}
