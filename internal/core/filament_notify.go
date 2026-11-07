package core

import (
	"github.com/ArseneGA/BAMBULAB-MCP/internal/filament"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/notify"
)

// FilamentNotification traduit un évènement de la file d'attente / des bobines en notification (queue_event).
// Le lien est celui de la page de confirmation humaine (queue_next_ready) ou de l'interface ; jamais de secret.
func FilamentNotification(nv *notify.Service, e filament.Event) notify.Event {
	titles := map[string]string{
		"queue_next_ready":  "Prochaine impression prête : à confirmer",
		"queue_item_failed": "File d'attente : une impression a échoué",
		"spool_low":         "Bobine presque vide",
	}
	t := titles[e.Kind]
	if t == "" {
		t = "File d'attente : " + e.Kind
	}
	link := e.ConfirmURL
	if link == "" {
		link = nv.BaseURL() + "/impression"
	}
	return notify.Event{Kind: notify.EvQueue, Title: t, Message: e.Message, Link: link, PrinterID: e.PrinterID, JobID: e.PrintJobID, At: e.At,
		DedupKey: "queue:" + e.Kind + ":" + e.QueueID + ":" + e.SpoolID + ":" + e.PrintJobID}
}
