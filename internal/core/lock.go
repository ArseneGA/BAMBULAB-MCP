package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrDataDirLocked : un autre processus utilise déjà ce dossier de données.
var ErrDataDirLocked = errors.New("le dossier de données est déjà utilisé par une autre instance de bambud")

// instanceLock est un verrou OS exclusif (LockFileEx / flock) sur le dossier de
// données : il évite deux services sur la même base (double Recover, deux
// workers qui rejoueraient ou lanceraient le même travail). Le système le
// libère de lui-même si le processus meurt.
type instanceLock struct{ f *os.File }

func acquireInstanceLock(dir string) (*instanceLock, error) {
	path := filepath.Join(dir, ".bambud.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("fichier de verrou : %w", err)
	}
	ok, err := tryLock(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("verrou du dossier de données : %w", err)
	}
	if !ok {
		f.Close()
		return nil, fmt.Errorf("%w (%s) : arrêtez l'autre instance ou choisissez un autre --data-dir", ErrDataDirLocked, dir)
	}
	return &instanceLock{f: f}, nil
}

// LockDataDir prend le verrou d'instance d'un dossier de données (utilisé par `bambud restore` pour ne
// jamais écraser les données d'un service en marche). La fonction renvoyée libère le verrou.
func LockDataDir(dir string) (release func() error, err error) {
	l, err := acquireInstanceLock(dir)
	if err != nil {
		return nil, err
	}
	return l.release, nil
}

// ErrLocked : le verrou demandé est déjà tenu par un autre processus.
var ErrLocked = errors.New("verrou déjà tenu par un autre processus")

// LockPath prend un verrou OS exclusif non bloquant sur le fichier donné (créé au besoin), libéré par le système si le
// processus meurt. Utilisé par la mise à jour automatique (une seule mise à jour à la fois ; flock(1) l'honore aussi).
func LockPath(path string) (release func() error, err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("fichier de verrou : %w", err)
	}
	ok, err := tryLock(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("verrou %s : %w", path, err)
	}
	if !ok {
		f.Close()
		return nil, fmt.Errorf("%w (%s)", ErrLocked, path)
	}
	l := &instanceLock{f: f}
	return l.release, nil
}

func (l *instanceLock) release() error {
	if l == nil || l.f == nil {
		return nil
	}
	_ = unlock(l.f)
	err := l.f.Close()
	l.f = nil
	return err
}
