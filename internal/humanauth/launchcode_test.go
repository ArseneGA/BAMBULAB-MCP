package humanauth_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

func launchStore(t *testing.T) (*store.Store, *clock) {
	t.Helper()
	ck := &clock{t: time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)}
	st, err := store.Open(filepath.Join(t.TempDir(), "l.db"), store.Options{Now: ck.now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st, ck
}

func TestLaunchCodeValidation(t *testing.T) {
	for _, bad := range []string{"", "12345", "123456789", "12345a", "123 456", "000000", "111111", "123456", "987654", "12345678"} {
		if err := humanauth.ValidateLaunchCode(bad); err == nil {
			t.Errorf("%q aurait dû être refusé", bad)
		}
	}
	for _, ok := range []string{"482916", "7340218", "90417352"} {
		if err := humanauth.ValidateLaunchCode(ok); err != nil {
			t.Errorf("%q refusé : %v", ok, err)
		}
	}
}

func TestLaunchCodeStoredHashedOnly(t *testing.T) {
	st, _ := launchStore(t)
	lc := humanauth.LaunchCodesFor(st)
	ctx := context.Background()
	if err := lc.Set(ctx, "482916", "human:test"); err != nil {
		t.Fatal(err)
	}
	var h string
	if err := st.DB().QueryRow(`SELECT code_hash FROM launch_code`).Scan(&h); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h, "482916") || !strings.HasPrefix(h, "argon2id$") {
		t.Fatalf("empreinte inattendue : %q", h)
	}
	// le journal d'audit ne contient jamais le code
	rows, _ := st.DB().Query(`SELECT COALESCE(detail,'') || operation FROM audit_log`)
	defer rows.Close()
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		if strings.Contains(s, "482916") {
			t.Fatalf("code dans le journal d'audit : %s", s)
		}
	}
	// deux définitions du même code donnent des empreintes différentes (sel)
	var h2 string
	_ = lc.Set(ctx, "482916", "human:test")
	_ = st.DB().QueryRow(`SELECT code_hash FROM launch_code`).Scan(&h2)
	if h == h2 {
		t.Fatal("empreinte identique : pas de sel")
	}
}

func TestLaunchCodeLockoutAfterThreeFailures(t *testing.T) {
	st, ck := launchStore(t)
	lc := humanauth.LaunchCodesFor(st)
	lc.SetMinDelay(0)
	ctx := context.Background()
	if err := lc.Verify(ctx, "482916", nil); !errors.Is(err, humanauth.ErrLaunchCodeNotSet) {
		t.Fatalf("sans code : %v", err)
	}
	_ = lc.Set(ctx, "482916", "human:test")
	// un essai mal formé n'est pas compté
	if err := lc.Verify(ctx, "abc", nil); !errors.Is(err, humanauth.ErrLaunchCodeFormat) {
		t.Fatalf("format : %v", err)
	}
	stt, _ := lc.Status(ctx)
	if stt.AttemptsLeft != 3 {
		t.Fatalf("un essai mal formé a été compté : %+v", stt)
	}
	for i, wantLeft := range []int{2, 1} {
		err := lc.Verify(ctx, "111222", nil)
		var bad *humanauth.BadLaunchCodeError
		if !errors.As(err, &bad) || bad.Left != wantLeft || bad.Locked {
			t.Fatalf("échec %d : %v", i+1, err)
		}
	}
	err := lc.Verify(ctx, "111222", nil)
	var bad *humanauth.BadLaunchCodeError
	if !errors.As(err, &bad) || !bad.Locked {
		t.Fatalf("3e échec : %v", err)
	}
	// verrouillé : même le bon code est refusé, quel que soit le temps écoulé
	ck.add(48 * time.Hour)
	if err := lc.Verify(ctx, "482916", nil); !errors.Is(err, humanauth.ErrLaunchCodeLocked) {
		t.Fatalf("bon code après verrouillage : %v", err)
	}
	// la page humaine / la CLI déverrouillent, le code reste le même
	if err := lc.Unlock(ctx, "human:cli"); err != nil {
		t.Fatal(err)
	}
	if err := lc.Verify(ctx, "482916", nil); err != nil {
		t.Fatalf("bon code après déverrouillage : %v", err)
	}
	// une réussite remet le compteur à zéro
	_ = lc.Verify(ctx, "111222", nil)
	if err := lc.Verify(ctx, "482916", nil); err != nil {
		t.Fatal(err)
	}
	if s, _ := lc.Status(ctx); s.AttemptsLeft != 3 {
		t.Fatalf("compteur non remis à zéro : %+v", s)
	}
}

func TestLaunchCodeMinimumDelayBetweenAttempts(t *testing.T) {
	st, ck := launchStore(t)
	lc := humanauth.LaunchCodesFor(st)
	ctx := context.Background()
	_ = lc.Set(ctx, "482916", "human:test")
	if err := lc.Verify(ctx, "111222", nil); !errors.Is(err, humanauth.ErrLaunchCodeBad) {
		t.Fatal(err)
	}
	// 2e essai immédiat : refusé SANS compter ni comparer
	if err := lc.Verify(ctx, "482916", nil); !errors.Is(err, humanauth.ErrLaunchCodeTooFast) {
		t.Fatalf("délai minimal : %v", err)
	}
	if s, _ := lc.Status(ctx); s.AttemptsLeft != 2 {
		t.Fatalf("un essai trop rapide a été compté : %+v", s)
	}
	ck.add(humanauth.DefaultLaunchAttemptDelay + time.Second)
	if err := lc.Verify(ctx, "482916", nil); err != nil {
		t.Fatalf("après le délai : %v", err)
	}
}

func TestLaunchCodeParallelGuessesAreCountedOnce(t *testing.T) {
	st, _ := launchStore(t)
	lc := humanauth.LaunchCodesFor(st)
	lc.SetMinDelay(0)
	ctx := context.Background()
	_ = lc.Set(ctx, "482916", "human:test")
	done := make(chan error, 12)
	for i := 0; i < 12; i++ {
		go func(i int) { done <- lc.Verify(ctx, "10000"+string(rune('0'+i%10)), nil) }(i)
	}
	bads := 0
	for i := 0; i < 12; i++ {
		var b *humanauth.BadLaunchCodeError
		if err := <-done; errors.As(err, &b) {
			bads++
		}
	}
	if bads != humanauth.LaunchMaxFailures {
		t.Fatalf("%d essais comparés pour %d autorisés", bads, humanauth.LaunchMaxFailures)
	}
	if s, _ := lc.Status(ctx); !s.Locked {
		t.Fatal("non verrouillé")
	}
}

func TestLaunchCodeRevoke(t *testing.T) {
	st, _ := launchStore(t)
	lc := humanauth.LaunchCodesFor(st)
	ctx := context.Background()
	_ = lc.Set(ctx, "482916", "human:test")
	if s, _ := lc.Status(ctx); !s.Set {
		t.Fatal("code non défini")
	}
	if err := lc.Revoke(ctx, "human:cli"); err != nil {
		t.Fatal(err)
	}
	if s, _ := lc.Status(ctx); s.Set {
		t.Fatal("code encore défini")
	}
	if err := lc.Verify(ctx, "482916", nil); !errors.Is(err, humanauth.ErrLaunchCodeNotSet) {
		t.Fatalf("après révocation : %v", err)
	}
}

// ---- page humaine ----

func launchServer(t *testing.T) (*httptest.Server, *humanauth.Service, *http.Client, *store.Store) {
	t.Helper()
	st, _ := launchStore(t)
	svc := humanauth.New(st, humanauth.Options{})
	mux := http.NewServeMux()
	svc.RegisterPages(mux)
	svc.RegisterLaunchCodePages(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	return ts, svc, &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, st
}

func nav(cl *http.Client, method, u string, form url.Values, site string) (int, string, http.Header) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, u, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", site)
	res, err := cl.Do(req)
	if err != nil {
		panic(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b), res.Header
}

var launchNonce = regexp.MustCompile(`name="nonce" value="([^"]+)"`)

func enrolled(t *testing.T, ts *httptest.Server, svc *humanauth.Service, cl *http.Client) {
	t.Helper()
	code, _, err := svc.NewEnrollCode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st, body, _ := nav(cl, "POST", ts.URL+"/enroll", url.Values{"code": {code}}, "same-origin"); st != 200 {
		t.Fatalf("enroll %d %s", st, body)
	}
}

func TestLaunchCodePageNeedsRegisteredBrowserAndNonce(t *testing.T) {
	ts, svc, cl, st := launchServer(t)
	// sans cookie : redirigé vers /enroll, et un POST est refusé
	if code, _, h := nav(cl, "GET", ts.URL+"/setup/launch-code", nil, "none"); code != http.StatusSeeOther || !strings.HasPrefix(h.Get("Location"), "/enroll") {
		t.Fatalf("GET anonyme : %d %v", code, h)
	}
	if code, _, _ := nav(cl, "POST", ts.URL+"/setup/launch-code", url.Values{"action": {"set"}, "code": {"482916"}, "code2": {"482916"}}, "same-origin"); code != http.StatusForbidden {
		t.Fatalf("POST anonyme : %d", code)
	}
	enrolled(t, ts, svc, cl)
	_, page, _ := nav(cl, "GET", ts.URL+"/setup/launch-code", nil, "none")
	m := launchNonce.FindStringSubmatch(page)
	if m == nil || !strings.Contains(page, "Ne tape jamais ce code dans la conversation") {
		t.Fatalf("page sans nonce ou sans mise en garde : %s", page)
	}
	set := func(nonce, c1, c2 string, site string) (int, string) {
		code, body, _ := nav(cl, "POST", ts.URL+"/setup/launch-code", url.Values{"nonce": {nonce}, "action": {"set"}, "code": {c1}, "code2": {c2}}, site)
		return code, body
	}
	// cross-site : refusé
	if code, _ := set(m[1], "482916", "482916", "cross-site"); code != http.StatusForbidden {
		t.Fatalf("cross-site : %d", code)
	}
	// nonce invalide / réutilisé
	if code, _ := set("faux", "482916", "482916", "same-origin"); code != http.StatusForbidden {
		t.Fatalf("faux nonce : %d", code)
	}
	_, page, _ = nav(cl, "GET", ts.URL+"/setup/launch-code", nil, "none")
	n1 := launchNonce.FindStringSubmatch(page)[1]
	if code, body := set(n1, "482916", "482917", "same-origin"); code != 400 || !strings.Contains(body, "identiques") {
		t.Fatalf("saisies différentes : %d", code)
	}
	_, page, _ = nav(cl, "GET", ts.URL+"/setup/launch-code", nil, "none")
	n2 := launchNonce.FindStringSubmatch(page)[1]
	if code, body := set(n2, "111111", "111111", "same-origin"); code != 400 || !strings.Contains(body, "trop simple") {
		t.Fatalf("code faible : %d", code)
	}
	_, page, _ = nav(cl, "GET", ts.URL+"/setup/launch-code", nil, "none")
	n3 := launchNonce.FindStringSubmatch(page)[1]
	code, body := set(n3, "482916", "482916", "same-origin")
	if code != 200 || !strings.Contains(body, "Code enregistré") {
		t.Fatalf("définition : %d %s", code, body)
	}
	if strings.Contains(body, "482916") {
		t.Fatal("la page renvoie le code")
	}
	if code, _ := set(n3, "482916", "482916", "same-origin"); code != http.StatusForbidden {
		t.Fatalf("nonce réutilisé : %d", code)
	}
	if s, _ := humanauth.LaunchCodesFor(st).Status(context.Background()); !s.Set {
		t.Fatal("code non défini")
	}
}

func TestLaunchCodePageUnlockAndRevoke(t *testing.T) {
	ts, svc, cl, st := launchServer(t)
	enrolled(t, ts, svc, cl)
	lc := humanauth.LaunchCodesFor(st)
	lc.SetMinDelay(0)
	ctx := context.Background()
	_ = lc.Set(ctx, "482916", "human:test")
	for i := 0; i < 3; i++ {
		_ = lc.Verify(ctx, "111222", nil)
	}
	act := func(action string) string {
		_, page, _ := nav(cl, "GET", ts.URL+"/setup/launch-code", nil, "none")
		n := launchNonce.FindStringSubmatch(page)[1]
		_, body, _ := nav(cl, "POST", ts.URL+"/setup/launch-code", url.Values{"nonce": {n}, "action": {action}}, "same-origin")
		return body
	}
	_, page, _ := nav(cl, "GET", ts.URL+"/setup/launch-code", nil, "none")
	if !strings.Contains(page, "VERROUILLÉ") {
		t.Fatalf("la page n'indique pas le verrouillage : %s", page)
	}
	if body := act("unlock"); !strings.Contains(body, "Verrou levé") {
		t.Fatalf("unlock : %s", body)
	}
	if s, _ := lc.Status(ctx); s.Locked || !s.Set {
		t.Fatalf("après unlock : %+v", s)
	}
	if body := act("revoke"); !strings.Contains(body, "révoqué") {
		t.Fatalf("revoke : %s", body)
	}
	if s, _ := lc.Status(ctx); s.Set {
		t.Fatal("code encore défini")
	}
}
