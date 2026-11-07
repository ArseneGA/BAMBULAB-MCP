package core

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/camera"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/filament"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/notify"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
)

// wireCamera branche la caméra (instantanés, détection d'échec, photo de /confirm) et les notifications sur la chaîne
// d'impression. Aucun des deux ne peut lancer ni arrêter une impression : ils observent et alertent.
func (c *Core) wireCamera() error {
	port := c.Cfg.CameraPort
	if port == 0 {
		if v, err := strconv.Atoi(os.Getenv("BAMBU_CAMERA_PORT")); err == nil && v > 0 && v < 65536 {
			port = v // simulateur : le port caméra n'est pas 6000
		}
	}
	nv, err := notify.New(notify.Deps{Store: c.Store, Vault: c.Printing.Secrets(), Log: c.Log, BaseURL: c.Printing.BaseURL})
	if err != nil {
		return fmt.Errorf("notifications : %w", err)
	}
	be := camera.PrintingBackend{P: c.Printing}
	cam, err := camera.New(camera.Config{Store: c.Store, Blobs: c.Blobs, Backend: be, Notifier: nv, Log: c.Log, Port: port, MinInterval: c.Cfg.CameraMinInterval})
	if err != nil {
		return fmt.Errorf("caméra : %w", err)
	}
	c.Notify, c.Camera = nv, cam
	c.Printing.SetPlatePhotoProvider(func(ctx context.Context, printerID string) printing.PlatePhoto {
		url, at, err := cam.PlatePhoto(ctx, printerID)
		if err != nil {
			return printing.PlatePhoto{Err: err.Error()}
		}
		return printing.PlatePhoto{URL: url, TakenAt: at}
	})
	cam.AttachEvents(be.Subscribe)
	w := notify.NewWatcher(nv, c.Printing, cam)
	w.Attach(c.Printing)
	if c.Filament != nil {
		c.Filament.Subscribe(func(e filament.Event) { nv.Emit(context.Background(), FilamentNotification(nv, e)) })
	}
	c.AddRunHook(cam.Run)
	c.AddRunHook(w.Run)
	c.AddCloseHook(cam.Close)
	return nil
}
