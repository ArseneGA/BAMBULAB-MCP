package contracts

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRedactJSON(t *testing.T) {
	in := json.RawMessage(`{"ip":"10.0.0.2","access_code":"12345678","Password":"x","n":{"secret_token":"s","keep":[{"secret_x":1,"ok":2.50}]},"big":12345678901234567890}`)
	if !ContainsSecret(in) {
		t.Fatal("secret non détecté")
	}
	out := string(RedactJSON(in))
	for _, bad := range []string{"12345678\"", "access_code", "Password", "secret_", "\"s\""} {
		if strings.Contains(out, bad) {
			t.Errorf("%q subsiste : %s", bad, out)
		}
	}
	for _, good := range []string{`"ip":"10.0.0.2"`, `"ok":2.50`, "12345678901234567890"} {
		if !strings.Contains(out, good) {
			t.Errorf("%q perdu : %s", good, out)
		}
	}
	clean := json.RawMessage(`{"a":1}`)
	if string(RedactJSON(clean)) != `{"a":1}` || ContainsSecret(clean) {
		t.Error("JSON propre modifié")
	}
	if string(RedactJSON(json.RawMessage(`{pas json`))) != `{pas json` {
		t.Error("JSON invalide doit être renvoyé tel quel")
	}
	j := Job{Params: in, Result: json.RawMessage(`{"password":"p"}`)}.Redacted()
	if ContainsSecret(j.Params) || ContainsSecret(j.Result) {
		t.Error("Job.Redacted incomplet")
	}
}

func TestConfirmationRejectsDuplicateSlotsAndBadTTL(t *testing.T) {
	now := time.Now()
	dup := binding()
	dup.Materials = append(dup.Materials, SlotAssignment{Slot: "ams1:1", MaterialID: "autre"})
	if _, _, err := NewConfirmation("c", "j", dup, time.Minute, now); !errors.Is(err, ErrConfirmationInvalid) {
		t.Errorf("slots dupliqués : %v", err)
	}
	for _, ttl := range []time.Duration{0, -time.Second, 24*time.Hour + time.Nanosecond, 1000 * time.Hour} {
		if _, _, err := NewConfirmation("c", "j", binding(), ttl, now); !errors.Is(err, ErrConfirmationInvalid) {
			t.Errorf("ttl %s : %v", ttl, err)
		}
	}
	if _, _, err := NewConfirmation("c", "j", binding(), 24*time.Hour, now); err != nil {
		t.Errorf("ttl 24 h refusé : %v", err)
	}
	// Une liaison courante invalide (slots dupliqués) ne consomme rien.
	c, tok, _ := NewConfirmation("c", "j", binding(), time.Minute, now)
	if err := c.Consume(tok, dup, now); !errors.Is(err, ErrConfirmationMismatch) || c.UsedAt != nil {
		t.Errorf("liaison invalide : %v", err)
	}
}
