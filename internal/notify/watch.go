package notify

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
)

// PhotoSource fournit la photo de fin d'impression (implémentée par internal/camera). Facultatif.
type PhotoSource interface {
	// FinalPhoto prend (ou relit) la photo de fin de l'impression ; photoURL est la route de l'interface qui la sert.
	FinalPhoto(ctx context.Context, printerID, jobID string) (jpeg []byte, photoURL string, err error)
}

// PrinterSource : ce que le surveillant lit sur le parc d'imprimantes (implémenté par *printing.Service).
type PrinterSource interface {
	ListPrinters(ctx context.Context) ([]printing.PrinterView, error)
	GetPrinterStatus(ctx context.Context, printerID string) (*printing.PrinterStatus, error)
	GetPrinterRecord(ctx context.Context, id string) (printing.PrinterRecord, error)
}

// Watcher transforme les évènements d'impression et la perte de contact avec une imprimante en notifications.
type Watcher struct {
	n      *Service
	p      PrinterSource
	photos PhotoSource

	// Interval : période de la vérification de joignabilité (défaut 30 s) ; OfflineAfter : échecs consécutifs avant
	// d'annoncer « hors ligne » (défaut 3).
	Interval     time.Duration
	OfflineAfter int

	mu      sync.Mutex
	misses  map[string]int
	offline map[string]bool
}

// NewWatcher construit le surveillant. photos peut être nil.
func NewWatcher(n *Service, p PrinterSource, photos PhotoSource) *Watcher {
	return &Watcher{n: n, p: p, photos: photos, Interval: 30 * time.Second, OfflineAfter: 3, misses: map[string]int{}, offline: map[string]bool{}}
}

// Attach abonne le surveillant aux transitions d'impression : fin, échec, lancement incertain.
func (w *Watcher) Attach(p *printing.Service) {
	p.OnEvent(w.OnPrintEvent)
	p.OnAlert(w.OnPrintAlert)
}

// OnPrintAlert traduit une alerte de supervision (pause sur erreur, impression bloquée, injoignable, arrêt non appliqué)
// en notification « print_attention ». Une alerte n'est pas une transition : l'impression garde son état.
func (w *Watcher) OnPrintAlert(a printing.Alert) {
	name := a.JobName
	if name == "" {
		name = a.JobID
	}
	pname := w.printerName(a.PrinterID)
	title := map[string]string{
		"attention":        "Action requise à l'écran : " + name,
		"stalled":          "Impression bloquée : " + name,
		"unreachable":      "Imprimante injoignable : " + name,
		"stop_not_applied": "Arrêt non appliqué : " + name,
	}[a.Kind]
	if title == "" {
		title = "À voir : " + name
	}
	go func() {
		defer func() { _ = recover() }()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		w.n.Emit(ctx, Event{Kind: EvPrintAttention, Title: title, Message: fmt.Sprintf("%s (%s) : %s", name, pname, a.Message), Link: w.n.BaseURL() + "/impression",
			PrinterID: a.PrinterID, JobID: a.JobID, At: a.At, DedupKey: EvPrintAttention + ":" + a.JobID + ":" + a.Kind + ":" + a.Message})
	}()
}

// OnPrintEvent traduit une transition de la machine d'états en notification.
func (w *Watcher) OnPrintEvent(ev printing.Event) {
	var kind, title, msg string
	name := ev.JobName
	if name == "" {
		name = ev.JobID
	}
	pname := w.printerName(ev.PrinterID)
	switch ev.To {
	case contracts.PrintCompleted:
		kind, title = EvPrintCompleted, "Impression terminée : "+name
		msg = fmt.Sprintf("%s est terminée sur %s. Pense à vérifier la pièce avant de retirer le plateau.", name, pname)
	case contracts.PrintFailed:
		kind, title = EvPrintFailed, "Échec d'impression : "+name
		msg = fmt.Sprintf("%s a échoué sur %s. %s", name, pname, ev.Note)
	case contracts.PrintStartUnknown:
		kind, title = EvStartUnknown, "Lancement incertain : "+name
		msg = fmt.Sprintf("Le résultat du lancement de %s sur %s est inconnu. Ne relance pas : demande à Claude de réconcilier (reconcile_print) ou vérifie l'imprimante. %s", name, pname, ev.Note)
	default:
		return
	}
	go func() {
		defer func() { _ = recover() }()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		e := Event{Kind: kind, Title: title, Message: msg, Link: w.n.BaseURL() + "/impression", PrinterID: ev.PrinterID, JobID: ev.JobID, At: ev.At,
			DedupKey: kind + ":" + ev.JobID}
		if w.photos != nil && (kind == EvPrintCompleted || kind == EvPrintFailed) {
			if jpg, url, err := w.photos.FinalPhoto(ctx, ev.PrinterID, ev.JobID); err == nil {
				e.Photo = jpg
				if url != "" {
					e.PhotoURL = w.n.BaseURL() + url
				}
			}
		}
		w.n.Emit(ctx, e)
	}()
}

func (w *Watcher) printerName(id string) string {
	if w.p == nil {
		return id
	}
	if rec, err := w.p.GetPrinterRecord(context.Background(), id); err == nil && rec.Name != "" {
		return rec.Name
	}
	return id
}

// Run surveille la joignabilité des imprimantes configurées jusqu'à l'annulation de ctx.
func (w *Watcher) Run(ctx context.Context) {
	if w.p == nil {
		return
	}
	t := time.NewTicker(w.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.Check(ctx)
		}
	}
}

// Check fait un tour de vérification (exporté pour les tests).
func (w *Watcher) Check(ctx context.Context) {
	ps, err := w.p.ListPrinters(ctx)
	if err != nil {
		return
	}
	for _, p := range ps {
		if !p.AccessCodeSet || p.PinnedSHA256 == "" {
			continue // pas encore configurée par l'humain : rien à surveiller
		}
		st, err := w.p.GetPrinterStatus(ctx, p.ID)
		up := err == nil && st != nil && st.Connected && st.State != "offline"
		w.mu.Lock()
		if up {
			w.misses[p.ID] = 0
			w.offline[p.ID] = false
			w.mu.Unlock()
			continue
		}
		w.misses[p.ID]++
		fire := w.misses[p.ID] >= w.OfflineAfter && !w.offline[p.ID]
		if fire {
			w.offline[p.ID] = true
		}
		w.mu.Unlock()
		if fire {
			w.n.EmitAsync(Event{Kind: EvPrinterOffline, Title: "Imprimante hors ligne : " + p.Name,
				Message: fmt.Sprintf("%s ne répond plus depuis plus de %s (éteinte, hors du réseau ou code d'accès changé ?).", p.Name, time.Duration(w.OfflineAfter)*w.Interval),
				Link:    w.n.BaseURL() + "/impression", PrinterID: p.ID, DedupKey: "offline:" + p.ID})
		}
	}
}
