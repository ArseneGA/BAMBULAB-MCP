package jobs

import (
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
)

func contractsJob(kind, state string) contracts.Job {
	return contracts.Job{Kind: kind, State: contracts.JobState(state)}
}

func TestBarShapes(t *testing.T) {
	for pct, want := range map[int]string{0: "░░░░░░░░░░", 1: "█░░░░░░░░░", 72: "███████░░░", 99: "█████████░", 100: "██████████", -5: "░░░░░░░░░░", 250: "██████████"} {
		if got := Bar(pct, BarWidth); got != want {
			t.Errorf("Bar(%d) = %q, attendu %q", pct, got, want)
		}
	}
}

func TestFormatters(t *testing.T) {
	if got := FormatMB(22649242); got != "21,6" {
		t.Fatalf("FormatMB = %q", got)
	}
	if got := FormatMB(30 << 20); got != "30" {
		t.Fatalf("FormatMB(30 Mo) = %q", got)
	}
	for sec, want := range map[int]string{-3: "~0 s", 45: "~45 s", 60: "~1 min", 200: "~3 min", 4000: "~67 min", 7500: "~2 h 05"} {
		if got := FormatETA(sec); got != want {
			t.Errorf("FormatETA(%d) = %q, attendu %q", sec, got, want)
		}
	}
}

// La ligne exacte demandée : « Dépôt sur la P1S : ███████░░░ 72 % (21,6 / 30 Mo, ~1 min) ».
func TestProgressTextLineUpload(t *testing.T) {
	eta := 60
	p := Progress{State: "running", Percent: 72, BytesDone: 22649242, BytesTotal: 30 << 20, ETASeconds: &eta, Phase: "envoi FTPS"}
	p.finalize(contractsJob("upload_print_artifact", "running"), "Dépôt sur la P1S")
	want := "Dépôt sur la P1S : ███████░░░ 72 % (21,6 / 30 Mo, ~1 min)"
	if p.Text != want || p.PhaseCode != "uploading" || p.DoneBytes != p.BytesDone || p.TotalBytes != 30<<20 || p.PhaseLabel != "Dépôt sur la P1S" || p.Phase != "envoi FTPS" {
		t.Fatalf("texte %q (attendu %q) ; %+v", p.Text, want, p)
	}
}

func TestProgressTextLineSliceEstimatedAndQueued(t *testing.T) {
	eta := 100
	p := Progress{State: "running", Percent: 30, Estimated: true, ETASeconds: &eta, Phase: "découpe par Bambu Studio"}
	p.finalize(contractsJob("printing.slice", "running"), "")
	if p.PhaseCode != "slicing" || p.Text != "Découpe par Bambu Studio : ███░░░░░░░ 30 % (estimation, ~2 min)" {
		t.Fatalf("%q %+v", p.Text, p)
	}
	pos := 2
	q := Progress{State: "queued", QueuePosition: &pos}
	q.finalize(contractsJob("printing.slice", "queued"), "")
	if q.PhaseCode != "queued" || q.Text != "En file d'attente (position 2)" {
		t.Fatalf("%q %+v", q.Text, q)
	}
}
