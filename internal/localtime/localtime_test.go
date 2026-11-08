package localtime

import (
	"testing"
	"time"
)

func TestResolveOrder(t *testing.T) {
	l, w := resolve("Europe/Paris", "America/New_York")
	if l.String() != "Europe/Paris" || w != "" {
		t.Fatalf("BAMBU_TIMEZONE doit primer : %v %q", l, w)
	}
	l, _ = resolve("", "Asia/Tokyo")
	if l.String() != "Asia/Tokyo" {
		t.Fatalf("TZ : %v", l)
	}
	l, w = resolve("Nope/Nowhere", "Europe/Paris")
	if l.String() != "Europe/Paris" || w == "" {
		t.Fatalf("nom invalide : repli sur TZ avec avertissement : %v %q", l, w)
	}
	l, _ = resolve("", "")
	if l != time.Local {
		t.Fatal("repli sur time.Local")
	}
}

func TestFormatsParis(t *testing.T) {
	t.Setenv("BAMBU_TIMEZONE", "Europe/Paris")
	Reload()
	u := time.Date(2026, 11, 8, 20, 3, 26, 0, time.UTC) // UTC+1 en novembre
	if got := Clock(u); got != "21:03:26" {
		t.Fatalf("Clock = %s", got)
	}
	if got := Page(u); got != "21:03:26 (08/11/2026)" {
		t.Fatalf("Page = %s", got)
	}
	if got := Full(u); got != "21:03 (08/11/2026, Europe/Paris)" {
		t.Fatalf("Full = %s", got)
	}
	if got := Age(u, u.Add(12*time.Second)); got != "il y a 12 s" {
		t.Fatalf("Age = %s", got)
	}
	if got := Age(u, u.Add(5*time.Minute)); got != "il y a 5 min" {
		t.Fatalf("Age = %s", got)
	}
}
