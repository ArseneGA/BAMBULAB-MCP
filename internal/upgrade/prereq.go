package upgrade

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

// ExitPrereq : code de sortie de `bambud upgrade` quand un prérequis de droits manque (le compte de construction ne peut pas
// traverser le dossier d'installation). Distinct de 1 (échec de mise à jour) et de 2 (usage, configuration).
const ExitPrereq = 3

// ErrBuilderAccess : le compte de construction n'a pas les droits sur le dossier d'installation.
var ErrBuilderAccess = errors.New("droits insuffisants pour le compte de construction")

// CheckBuilderAccess vérifie que le compte de construction peut TRAVERSER le dossier d'installation (et releases/ s'il existe) :
// il y crée le dossier partiel de la construction. Quand /opt/atelier est en 0750 root:atelier, sans ACL ni groupe, l'échec
// serait sinon « mkdir: cannot create directory '/opt/atelier': Permission denied » au milieu de la construction.
func CheckBuilderAccess(ctx context.Context, run Runner, cfg Config) error {
	for _, dir := range []string{cfg.Root, cfg.ReleasesDir()} {
		if _, err := os.Stat(dir); err != nil {
			continue // absent (ou illisible pour root : l'étape suivante le dira) : rien à traverser pour l'instant
		}
		env := []string{"HOME=" + cfg.CacheDir, "PATH=" + defaultPath}
		if _, err := run.Run(ctx, Cmd{Name: "test", Args: []string{"-x", dir}, Dir: "/", Env: env, User: cfg.BuildUser, Timeout: time.Minute}); err != nil {
			return fmt.Errorf("%w : le compte %s ne peut pas traverser %s. Correctif : sudo setfacl -m u:%s:--x %s "+
				"(ou relancer sudo bash deploy/linux/enable-auto-update.sh, qui le fait)", ErrBuilderAccess, cfg.BuildUser, dir, cfg.BuildUser, cfg.Root)
		}
	}
	return nil
}
