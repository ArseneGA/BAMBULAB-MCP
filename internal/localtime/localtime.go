// Package localtime centralise le fuseau horaire d'affichage : les pages humaines (/confirm, /stop, /setup…), les
// notifications et le texte destiné au modèle montrent l'heure LOCALE de l'utilisateur, pas l'UTC du serveur.
//
// Ordre de résolution : BAMBU_TIMEZONE (nom IANA, ex. Europe/Paris), puis TZ, puis time.Local (fuseau du système).
// Un nom invalide est ignoré (Warning le dit) : on retombe sur la suite de la chaîne.
// Les données stockées et les champs RFC 3339 des réponses MCP restent en UTC / avec décalage : seul l'affichage change.
package localtime

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // base IANA embarquée : fonctionne dans une image minimale ou sous Windows sans tzdata système
)

var (
	mu      sync.RWMutex
	loc     *time.Location
	warning string
)

func init() { Reload() }

// Reload relit BAMBU_TIMEZONE / TZ. Il aligne aussi time.Local, pour que les appels existants à .Local() suivent le réglage.
func Reload() {
	l, w := resolve(os.Getenv("BAMBU_TIMEZONE"), os.Getenv("TZ"))
	mu.Lock()
	loc, warning = l, w
	mu.Unlock()
	if l != nil && l != time.Local {
		time.Local = l
	}
}

func resolve(bambu, tz string) (*time.Location, string) {
	w := ""
	for _, c := range []struct{ name, v string }{{"BAMBU_TIMEZONE", bambu}, {"TZ", tz}} {
		v := strings.TrimPrefix(strings.TrimSpace(c.v), ":")
		if v == "" {
			continue
		}
		if l, err := time.LoadLocation(v); err == nil {
			return l, w
		}
		if w == "" {
			w = c.name + "=" + v + " : fuseau inconnu (attendu : nom IANA, ex. Europe/Paris) ; ignoré"
		}
	}
	return time.Local, w
}

// Location renvoie le fuseau d'affichage.
func Location() *time.Location {
	mu.RLock()
	defer mu.RUnlock()
	return loc
}

// Warning dit pourquoi un réglage a été ignoré (chaîne vide si tout va bien).
func Warning() string {
	mu.RLock()
	defer mu.RUnlock()
	return warning
}

// Name est le nom du fuseau (ex. « Europe/Paris »).
func Name() string { return Location().String() }

// In ramène t dans le fuseau d'affichage.
func In(t time.Time) time.Time { return t.In(Location()) }

// Clock : « 21:03:26 ».
func Clock(t time.Time) string { return In(t).Format("15:04:05") }

// Date : « 08/11/2026 » (format français).
func Date(t time.Time) string { return In(t).Format("02/01/2006") }

// Page : « 21:03:26 (08/11/2026) », pour les pages humaines.
func Page(t time.Time) string { return Clock(t) + " (" + Date(t) + ")" }

// Full : « 21:03 (08/11/2026, Europe/Paris) », pour les champs lisibles des réponses MCP et le texte du modèle.
func Full(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return In(t).Format("15:04") + " (" + Date(t) + ", " + Name() + ")"
}

// Age : « il y a 12 s » / « il y a 3 min » (jamais négatif).
func Age(t, now time.Time) string {
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < 90*time.Second:
		return "il y a " + strconv.Itoa(int(d.Seconds())) + " s"
	case d < 90*time.Minute:
		return "il y a " + strconv.Itoa(int(d.Minutes())) + " min"
	}
	return "il y a " + strconv.Itoa(int(d.Hours())) + " h"
}
