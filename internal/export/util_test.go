package export_test

import (
	"net/url"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

func queryOf(t *testing.T, raw, key string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get(key)
}

func store0(t time.Time) string { return store.FormatTime(t) }
