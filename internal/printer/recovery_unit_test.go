package printer

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestHMSFamiliesFromTheFieldAreDecoded(t *testing.T) {
	cases := []struct {
		attr, code uint32
		want       string
	}{
		{0x03000D00, 0x00020005, "mise à l'origine"},
		{0x07FF5000, 0x00020001, "bobine externe"},
		{0x07FF7000, 0x00020008, "bobine externe"},
		{0x07FF7000, 0x00020099, "bobine externe"}, // code inconnu mais famille connue
	}
	for _, c := range cases {
		i := DescribeHMS(HMSError{Attr: c.attr, Code: c.code})
		if !i.Known || !strings.Contains(i.Text, c.want) || !i.Unverified || i.Source == "" || i.Action == "" {
			t.Errorf("%08X%08X : %+v", c.attr, c.code, i)
		}
		if !strings.HasPrefix(i.Wiki, HMSWikiPrefix) || i.Severity != "grave" {
			t.Errorf("lien ou gravité : %+v", i)
		}
	}
	i := DescribeHMS(HMSError{Attr: 0x03000D00, Code: 0x00020005})
	if i.Short != "0300_0D00_0002_0005" || !strings.HasSuffix(i.Wiki, "0300_0D00_0002_0005") || i.SummaryLine() != "0300_0D00_0002_0005 (grave) : "+i.Text {
		t.Errorf("forme : %+v", i)
	}
	// print_error : 50348032 = 0300_4000 et 07FF_8012 ; 50348044 reste le reliquat bénin d'annulation (errcodes.go inchangé)
	if i := DescribePrintError(50348032); !i.Known || i.Short != "0300_4000" || i.Benign || !strings.Contains(i.Text, "mouvement") {
		t.Errorf("0300_4000 : %+v", i)
	}
	if i := DescribePrintError(0x07FF8012); !i.Known || !strings.Contains(i.Text, "bobine externe") {
		t.Errorf("07FF_8012 : %+v", i)
	}
	if i := DescribePrintError(50348044); !i.Benign || !strings.Contains(i.Text, "annulée") {
		t.Errorf("0300_400C : %+v", i)
	}
	// un code absent n'est jamais deviné
	if i := DescribeHMS(HMSError{Attr: 0x0C000100, Code: 0x00030001}); i.Known || !strings.Contains(i.Text, "(inconnu)") {
		t.Errorf("inconnu : %+v", i)
	}
}

func TestReconnectBackoffGrowsAndCaps(t *testing.T) {
	want := []time.Duration{2, 4, 8, 16, 32, 60, 60, 60}
	for i, w := range want {
		if got := reconnectBackoff(2*time.Second, ReconnectMaxDefault, i); got != w*time.Second {
			t.Errorf("essai %d : %s, attendu %ds", i, got, w)
		}
	}
}

func TestDiagnoseClassifiesNetworkServiceAndPrinter(t *testing.T) {
	ctx := context.Background()
	noARP := func(context.Context, string) (ArpEntry, error) { return ArpEntry{Via: "test"}, nil }
	withARP := func(context.Context, string) (ArpEntry, error) {
		return ArpEntry{Found: true, MAC: "aa:bb:cc:dd:ee:ff", Via: "test"}, nil
	}

	// port fermé sur l'hôte local : connexion refusée partout -> l'adresse répond, aucun service
	closed := func() int {
		l, _ := net.Listen("tcp", "127.0.0.1:0")
		p := l.Addr().(*net.TCPAddr).Port
		l.Close()
		return p
	}
	r := Diagnose(ctx, DiagTarget{Host: "127.0.0.1", MQTTPort: closed(), FTPPort: closed(), CameraPort: -1, ReadARP: noARP, Timeout: time.Second}, nil)
	if r.Layer != "printer" || !strings.Contains(r.Diagnosis, "refusée") {
		t.Errorf("refusé partout : %s / %s", r.Layer, r.Diagnosis)
	}
	// MQTT et FTPS ouverts, session de bambud coupée -> service
	l1, _ := net.Listen("tcp", "127.0.0.1:0")
	l2, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l1.Close()
	defer l2.Close()
	go func() {
		for {
			c, err := l1.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	go func() {
		for {
			c, err := l2.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	mp, fp := l1.Addr().(*net.TCPAddr).Port, l2.Addr().(*net.TCPAddr).Port
	down := &ConnInfo{Connected: false, DownSince: time.Now().Add(-time.Minute), LastError: "bad user name or password"}
	r = Diagnose(ctx, DiagTarget{Host: "127.0.0.1", MQTTPort: mp, FTPPort: fp, CameraPort: -1, ReadARP: withARP}, down)
	if r.Layer != "service" || !strings.Contains(r.Diagnosis, "session de bambud est coupée") || !strings.Contains(r.Diagnosis, "bad user name") {
		t.Errorf("session coupée : %s / %s", r.Layer, r.Diagnosis)
	}
	r = Diagnose(ctx, DiagTarget{Host: "127.0.0.1", MQTTPort: mp, FTPPort: fp, CameraPort: -1, ReadARP: withARP}, &ConnInfo{Connected: true})
	if r.Layer != "none" {
		t.Errorf("tout répond : %s / %s", r.Layer, r.Diagnosis)
	}
	// DNS en échec
	r = Diagnose(ctx, DiagTarget{Host: "imprimante.local", CameraPort: -1, ReadARP: noARP,
		Resolve: func(context.Context, string) ([]string, error) { return nil, errors.New("no such host") }}, nil)
	if r.Layer != "dns" || !strings.Contains(r.DNS, "no such host") {
		t.Errorf("dns : %s / %s", r.Layer, r.DNS)
	}
	// conclusion « réseau » : aucun port ne répond, pas d'ARP (sur des ports fermés d'une adresse qui n'existe pas)
	layer, text, _ := conclude(DiagReport{DNS: "sans objet (adresse IP)", Ports: []PortProbe{{Port: 8883, Kind: "no_route", Error: "dial tcp 192.168.0.12:8883: connect: no route to host"}, {Port: 990, Kind: "no_route"}, {Port: 6000, Kind: "timeout"}}}, nil)
	if layer != "network" || !strings.Contains(text, "no route to host") || !strings.Contains(text, "ARP") {
		t.Errorf("réseau : %s / %s", layer, text)
	}
}

func TestParseARPSources(t *testing.T) {
	proc := "IP address       HW type     Flags       HW address            Mask     Device\n192.168.0.12     0x1         0x2         aa:bb:cc:dd:ee:ff     *        eth0\n192.168.0.99     0x1         0x0         00:00:00:00:00:00     *        eth0\n"
	if e := parseProcARP(strings.NewReader(proc), "192.168.0.12"); !e.Found || e.MAC != "aa:bb:cc:dd:ee:ff" || e.State != "complète" {
		t.Errorf("proc arp : %+v", e)
	}
	if e := parseProcARP(strings.NewReader(proc), "192.168.0.99"); e.Found {
		t.Errorf("entrée incomplète : %+v", e)
	}
	win := "Interface : 192.168.0.5 --- 0x5\n  Adresse Internet      Adresse physique      Type\n  192.168.0.12          aa-bb-cc-dd-ee-ff     dynamique\n"
	if e := parseArpA(win, "192.168.0.12"); !e.Found || e.MAC != "aa-bb-cc-dd-ee-ff" {
		t.Errorf("arp -a Windows : %+v", e)
	}
	unix := "? (192.168.0.12) at aa:bb:cc:dd:ee:ff [ether] on eth0\n"
	if e := parseArpA(unix, "192.168.0.12"); !e.Found || e.MAC != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("arp -a Unix : %+v", e)
	}
}

func TestSafeRemoteName(t *testing.T) {
	for _, ok := range []string{"porte_guitare_supports_v3-0123456789ab.gcode.3mf", "box-0123456789ab.3mf"} {
		if sha, good := SafeRemoteName(ok); !good || len(sha) != 12 {
			t.Errorf("%s doit être valide", ok)
		}
	}
	for _, bad := range []string{"notes.txt", "../etc-0123456789ab.3mf", "a/b-0123456789ab.3mf", "box-XYZ.3mf", "box-0123456789ab.gcode", "a..b-0123456789ab.3mf"} {
		if _, good := SafeRemoteName(bad); good {
			t.Errorf("%s ne doit pas être supprimable", bad)
		}
	}
}
