package upgrade

import (
	"context"
	"time"
)

func (e *SysEnv) sleep(ctx context.Context, d time.Duration) error {
	if e.Sleep != nil {
		return e.Sleep(ctx, d)
	}
	return sleepCtx(ctx, d)
}
