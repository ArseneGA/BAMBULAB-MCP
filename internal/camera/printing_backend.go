package camera

import (
	"context"
	"errors"
	"fmt"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
)

// PrintingBackend adapte *printing.Service à Backend. Tout y est en lecture, sauf StopURL qui crée une DEMANDE d'arrêt
// (rien n'est arrêté tant que l'humain n'a pas validé la page /stop).
type PrintingBackend struct{ P *printing.Service }

type vaultSecret struct {
	p  *printing.Service
	id string
}

func (v vaultSecret) AccessCode(ctx context.Context) (string, error) {
	return v.p.Secrets().AccessCode(ctx, v.id)
}
func (vaultSecret) String() string   { return "AccessCode(***)" }
func (vaultSecret) GoString() string { return "AccessCode(***)" }

func (b PrintingBackend) Target(ctx context.Context, printerID string) (Target, error) {
	rec, err := b.P.GetPrinterRecord(ctx, printerID)
	if err != nil {
		if errors.Is(err, printing.ErrNotFound) {
			return Target{}, fmt.Errorf("%w : imprimante %q", ErrNotFound, printerID)
		}
		return Target{}, err
	}
	m, _ := b.P.GetPrinterModel(ctx, rec.ModelID)
	t := Target{ID: rec.ID, Name: rec.Name, Host: rec.Host, Serial: rec.Serial, ModelID: rec.ModelID, ModelName: m.Name, PinnedSHA256: rec.PinnedSHA256}
	if k, _ := printer.ParseLinkKind(rec.Link); k == printer.LinkAgent {
		// Imprimante servie par un agent local : l'image est relayée par l'agent (le serveur n'a pas le code d'accès).
		t.Snap = func(ctx context.Context) ([]byte, error) { return b.P.AgentSnapshot(ctx, printerID) }
		return t, nil
	}
	if b.P.Secrets().HasAccessCode(printerID) {
		t.Secrets = vaultSecret{b.P, printerID}
	}
	return t, nil
}

func (b PrintingBackend) PrintingJobs(ctx context.Context) ([]JobInfo, error) {
	js, err := b.P.ListJobs(ctx, contracts.PrintPrinting, 100)
	if err != nil {
		return nil, err
	}
	out := make([]JobInfo, 0, len(js))
	for _, j := range js {
		out = append(out, JobInfo{ID: j.ID, PrinterID: j.PrinterID, Name: j.Name, State: j.State})
	}
	return out, nil
}

func (b PrintingBackend) Job(ctx context.Context, jobID string) (JobInfo, error) {
	j, err := b.P.GetJob(ctx, jobID)
	if err != nil {
		return JobInfo{}, err
	}
	return JobInfo{ID: j.ID, PrinterID: j.PrinterID, Name: j.Name, State: j.State}, nil
}

func (b PrintingBackend) State(ctx context.Context, printerID string) (PrinterState, error) {
	st, err := b.P.GetPrinterStatus(ctx, printerID)
	if err != nil {
		return PrinterState{}, err
	}
	return PrinterState{Reachable: st.Reachable && st.State != string(printer.StateOffline), Printing: st.State == string(printer.StatePrinting),
		Layer: st.Layer, TotalLayers: st.TotalLayers, HMSCount: st.HMSCount, PrintError: st.PrintError, Percent: st.Percent}, nil
}

func (b PrintingBackend) StopURL(ctx context.Context, jobID string) (string, error) {
	if j, err := b.P.GetJob(ctx, jobID); err != nil || j.State != contracts.PrintPrinting {
		return "", err // jamais d'annulation logique d'une impression qui n'est pas en cours
	}
	o, err := b.P.CancelPrint(ctx, printing.CancelInput{PrintJobID: jobID})
	if err != nil {
		return "", err
	}
	return o.StopURL, nil // vide si l'impression n'est plus en cours ; aucun arrêt n'a eu lieu
}

func (b PrintingBackend) BaseURL() string { return b.P.BaseURL() }

// Subscribe branche les fins d'impression sur le service d'impression.
func (b PrintingBackend) Subscribe(fn func(PrintEvent)) {
	b.P.OnEvent(func(e printing.Event) { fn(PrintEvent{JobID: e.JobID, PrinterID: e.PrinterID, To: e.To}) })
}
