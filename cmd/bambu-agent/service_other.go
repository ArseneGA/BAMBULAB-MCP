//go:build !windows

package main

import "errors"

func cmdService([]string) error { return errors.New("le service Windows n'existe que sous Windows") }
