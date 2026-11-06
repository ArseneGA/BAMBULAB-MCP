package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestEnqueueRejectsSecretsInParams(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, t.TempDir())
	defer s.Close()
	q := NewQueue(s, QueueConfig{})
	for _, p := range []string{`{"access_code":"1234"}`, `{"printer":{"secret_key":"x"}}`, `{"list":[{"password":"p"}]}`} {
		if _, _, err := q.Enqueue(ctx, EnqueueParams{Kind: "x", Params: json.RawMessage(p)}); !errors.Is(err, ErrSecretInParams) {
			t.Errorf("%s : %v", p, err)
		}
	}
	if _, _, err := q.Enqueue(ctx, EnqueueParams{Kind: "x", Params: json.RawMessage(`{"ip":"10.0.0.2"}`)}); err != nil {
		t.Fatal(err)
	}
}
