package printer

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Diagnostic de connexion (lecture seule) : DNS, tentative TCP (équivalent de ping sans privilèges), entrée ARP et état
// des ports 8883 (MQTT), 990 (FTPS) et 6000 (caméra P1/A1). Ne contacte QUE l'hôte enregistré de l'imprimante, sur ces
// ports ; n'envoie aucune donnée applicative et ne lit aucun secret.

// DiagTarget : ce qu'on sonde.
type DiagTarget struct {
	Host       string
	MQTTPort   int // défaut 8883
	FTPPort    int // défaut 990
	CameraPort int // défaut 6000 ; 0 = défaut, -1 = ne pas sonder
	Timeout    time.Duration
	// ReadARP / Resolve : substituables (tests). Défauts : table ARP du système, net.DefaultResolver.
	ReadARP func(ctx context.Context, ip string) (ArpEntry, error)
	Resolve func(ctx context.Context, host string) ([]string, error)
}

// PortProbe : résultat de la tentative de connexion TCP sur un port.
type PortProbe struct {
	Port      int           `json:"port"`
	Role      string        `json:"role"`
	Open      bool          `json:"open"`
	LatencyMS int64         `json:"latency_ms,omitempty"`
	Error     string        `json:"error,omitempty"`
	Kind      string        `json:"kind,omitempty" jsonschema:"open | refused | no_route | timeout | other"`
	Elapsed   time.Duration `json:"-"`
}

// ArpEntry : ligne de la table ARP pour l'adresse.
type ArpEntry struct {
	Found bool   `json:"found"`
	MAC   string `json:"mac,omitempty"`
	State string `json:"state,omitempty"`
	Via   string `json:"via,omitempty" jsonschema:"source : /proc/net/arp, arp -a ou indisponible"`
	Note  string `json:"note,omitempty"`
}

// DiagReport est le résultat de Diagnose.
type DiagReport struct {
	Host      string      `json:"host"`
	Addresses []string    `json:"addresses,omitempty"`
	DNS       string      `json:"dns" jsonschema:"résolution du nom : ok, sans objet (adresse IP) ou l'erreur"`
	Ports     []PortProbe `json:"ports"`
	ARP       ArpEntry    `json:"arp"`
	Layer     string      `json:"layer" jsonschema:"où est la panne : none | dns | network | service | printer | unknown"`
	Diagnosis string      `json:"diagnosis" jsonschema:"diagnostic en français : réseau, service ou imprimante"`
	Advice    []string    `json:"advice,omitempty"`
}

func classifyDialError(err error) string {
	if err == nil {
		return "open"
	}
	m := strings.ToLower(err.Error())
	switch {
	case strings.Contains(m, "refused") || strings.Contains(m, "actively refused"):
		return "refused"
	case strings.Contains(m, "no route") || strings.Contains(m, "unreachable") || strings.Contains(m, "host is down") || strings.Contains(m, "network is down"):
		return "no_route"
	case strings.Contains(m, "timeout") || strings.Contains(m, "deadline") || strings.Contains(m, "timed out"):
		return "timeout"
	}
	return "other"
}

// Diagnose sonde l'imprimante et conclut (réseau, service ou imprimante).
func Diagnose(ctx context.Context, t DiagTarget, conn *ConnInfo) DiagReport {
	if t.MQTTPort == 0 {
		t.MQTTPort = 8883
	}
	if t.FTPPort == 0 {
		t.FTPPort = 990
	}
	if t.CameraPort == 0 {
		t.CameraPort = 6000
	}
	if t.Timeout <= 0 {
		t.Timeout = 3 * time.Second
	}
	rep := DiagReport{Host: t.Host}
	// 1. DNS
	ip := net.ParseIP(strings.Trim(t.Host, "[]"))
	switch {
	case ip != nil:
		rep.DNS, rep.Addresses = "sans objet (adresse IP)", []string{ip.String()}
	default:
		res := t.Resolve
		if res == nil {
			res = func(ctx context.Context, h string) ([]string, error) { return net.DefaultResolver.LookupHost(ctx, h) }
		}
		rctx, cancel := context.WithTimeout(ctx, t.Timeout)
		addrs, err := res(rctx, t.Host)
		cancel()
		if err != nil {
			rep.DNS = "échec : " + err.Error()
		} else {
			rep.DNS, rep.Addresses = "ok", addrs
		}
	}
	target := t.Host
	if len(rep.Addresses) > 0 {
		target = rep.Addresses[0]
	}
	// 2. ports (en parallèle)
	ports := []PortProbe{{Port: t.MQTTPort, Role: "MQTT (état et commandes)"}, {Port: t.FTPPort, Role: "FTPS (dépôt de fichiers)"}}
	if t.CameraPort > 0 {
		ports = append(ports, PortProbe{Port: t.CameraPort, Role: "caméra (P1/A1)"})
	}
	if len(rep.Addresses) > 0 || ip != nil {
		var wg sync.WaitGroup
		for i := range ports {
			wg.Add(1)
			go func(p *PortProbe) {
				defer wg.Done()
				d := net.Dialer{Timeout: t.Timeout}
				start := time.Now()
				c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(target, strconv.Itoa(p.Port)))
				p.Elapsed = time.Since(start)
				p.Kind = classifyDialError(err)
				if err != nil {
					p.Error = err.Error()
					return
				}
				p.Open, p.LatencyMS = true, p.Elapsed.Milliseconds()
				_ = c.Close()
			}(&ports[i])
		}
		wg.Wait()
	} else {
		for i := range ports {
			ports[i].Kind, ports[i].Error = "other", "non sondé : adresse non résolue"
		}
	}
	rep.Ports = ports
	// 3. ARP
	read := t.ReadARP
	if read == nil {
		read = ReadARPEntry
	}
	if ip != nil || len(rep.Addresses) > 0 {
		if e, err := read(ctx, target); err == nil {
			rep.ARP = e
		} else {
			rep.ARP = ArpEntry{Via: "indisponible", Note: err.Error()}
		}
	}
	rep.Layer, rep.Diagnosis, rep.Advice = conclude(rep, conn)
	return rep
}

func openCount(ps []PortProbe) (open int) {
	for _, p := range ps {
		if p.Open {
			open++
		}
	}
	return
}

func kindCount(ps []PortProbe, k string) (n int) {
	for _, p := range ps {
		if p.Kind == k {
			n++
		}
	}
	return
}

// conclude : diagnostic en français, du plus grave au plus bénin.
func conclude(r DiagReport, conn *ConnInfo) (layer, text string, advice []string) {
	if !strings.HasPrefix(r.DNS, "ok") && !strings.HasPrefix(r.DNS, "sans objet") {
		return "dns", "Le nom de l'imprimante ne se résout pas : c'est un problème de résolution de nom (DNS / mDNS), pas de l'imprimante.",
			[]string{"utilisez l'adresse IP de l'imprimante (écran > réseau) plutôt qu'un nom .local", "réservez cette adresse dans le routeur (bail DHCP fixe)"}
	}
	open := openCount(r.Ports)
	mqtt := r.Ports[0]
	switch {
	case open == 0 && kindCount(r.Ports, "refused") == len(r.Ports):
		return "printer", "L'adresse répond (connexion refusée) mais aucun service de l'imprimante n'écoute : elle est probablement en train de démarrer, ou le mode LAN est coupé.",
			[]string{"attendez 1 à 2 minutes après un redémarrage", "vérifiez sur l'écran que le « mode LAN » est activé et que le code d'accès est inchangé"}
	case open == 0:
		adv := []string{"vérifiez que l'imprimante est allumée et affiche le Wi-Fi connecté (écran > réseau)",
			"après un redémarrage, une P1S peut mettre plusieurs minutes à rejoindre le Wi-Fi ; si c'est plus long, rapprochez-la du routeur, redémarrez le routeur ou ré-associez-la",
			"un bail DHCP a pu changer l'adresse : comparez avec l'adresse affichée sur l'écran (update de l'hôte sur la page d'imprimante)"}
		if r.ARP.Found {
			return "network", "L'adresse est connue du réseau local (entrée ARP) mais AUCUN port ne répond (" + dialSummary(r.Ports) + ") : l'imprimante est sans doute éteinte, endormie ou en cours de reconnexion Wi-Fi ; l'entrée ARP peut être périmée.", adv
		}
		return "network", "Réseau : l'imprimante n'est pas joignable (" + dialSummary(r.Ports) + ") et aucune entrée ARP ne la signale : elle n'est pas (ou plus) sur le réseau local (éteinte, Wi-Fi perdu, adresse changée).", adv
	case !mqtt.Open:
		return "service", "L'imprimante répond sur le réseau mais le port MQTT " + strconv.Itoa(mqtt.Port) + " est fermé : le service d'état de l'imprimante ne répond pas (démarrage en cours, mode LAN coupé ou micrologiciel bloqué).",
			[]string{"vérifiez le mode LAN sur l'écran", "redémarrez l'imprimante si cela dure plus de quelques minutes"}
	}
	// MQTT ouvert
	if conn != nil && !conn.Connected {
		since := ""
		if !conn.DownSince.IsZero() {
			since = " depuis " + conn.DownSince.Local().Format("15:04:05")
		}
		le := ""
		if conn.LastError != "" {
			le = " (dernière erreur : " + conn.LastError + ")"
		}
		return "service", "Le port MQTT répond mais la session de bambud est coupée" + since + le + " : problème de service (code d'accès changé, certificat renouvelé, trop de clients connectés) ; la reconnexion automatique reprend dès que la session passe.",
			[]string{"vérifiez le code d'accès et l'empreinte du certificat sur la page /setup/printer/<id>", "fermez Bambu Studio / Handy / un autre client qui monopolise la connexion"}
	}
	if !r.Ports[1].Open {
		return "service", "MQTT répond (état et commandes possibles) mais le port FTPS " + strconv.Itoa(r.Ports[1].Port) + " est fermé : le dépôt de fichiers est impossible pour l'instant.", []string{"réessayez dans une minute ; redémarrez l'imprimante si cela dure"}
	}
	return "none", "Tout répond : réseau, MQTT et FTPS sont joignables.", nil
}

func dialSummary(ps []PortProbe) string {
	var parts []string
	for _, p := range ps {
		e := p.Kind
		if p.Error != "" {
			e = p.Error
		}
		parts = append(parts, fmt.Sprintf("%d : %s", p.Port, e))
	}
	return strings.Join(parts, " ; ")
}

// ReadARPEntry lit l'entrée ARP d'une adresse IPv4 : /proc/net/arp sous Linux, `arp -a` ailleurs. Aucun secret.
func ReadARPEntry(ctx context.Context, ip string) (ArpEntry, error) {
	if runtime.GOOS == "linux" {
		f, err := os.Open("/proc/net/arp")
		if err == nil {
			defer f.Close()
			return parseProcARP(f, ip), nil
		}
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, "arp", "-a").Output()
	if err != nil {
		return ArpEntry{Via: "indisponible"}, fmt.Errorf("arp -a indisponible : %w", err)
	}
	return parseArpA(string(out), ip), nil
}

// parseProcARP lit le format de /proc/net/arp : IP address, HW type, Flags, HW address, Mask, Device.
func parseProcARP(r io.Reader, ip string) ArpEntry {
	sc := bufio.NewScanner(r)
	e := ArpEntry{Via: "/proc/net/arp"}
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || f[0] != ip {
			continue
		}
		e.Found = true
		e.MAC = f[3]
		switch f[2] {
		case "0x2":
			e.State = "complète"
		case "0x0":
			e.State = "incomplète (aucune réponse)"
			if e.MAC == "00:00:00:00:00:00" {
				e.Found = false
			}
		default:
			e.State = "drapeaux " + f[2]
		}
		return e
	}
	return e
}

// parseArpA lit la sortie de `arp -a` (Windows : « 192.168.0.12  aa-bb-cc-dd-ee-ff  dynamique » ; Unix : « ? (192.168.0.12) at aa:bb... »).
func parseArpA(out, ip string) ArpEntry {
	e := ArpEntry{Via: "arp -a"}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		hit := false
		for _, w := range f {
			if strings.Trim(w, "()") == ip {
				hit = true
			}
		}
		if !hit {
			continue
		}
		for _, w := range f {
			if len(w) >= 11 && (strings.Count(w, ":") == 5 || strings.Count(w, "-") == 5) {
				e.Found, e.MAC = true, strings.ToLower(w)
			}
		}
		if strings.Contains(strings.ToLower(line), "incomplete") {
			e.Found, e.State = false, "incomplète"
		}
		return e
	}
	return e
}
