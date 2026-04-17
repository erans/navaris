//go:build linux || darwin
// +build linux darwin

package main

import (
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"
)

func termSetRaw(fd int) (any, error) {
	return term.MakeRaw(fd)
}

func termRestore(fd int, state any) {
	if s, ok := state.(*term.State); ok {
		_ = term.Restore(fd, s)
	}
}

func termSize(fd int) (int, int, error) {
	return term.GetSize(fd)
}

func signalNotify(c chan os.Signal, sigs ...os.Signal) {
	signal.Notify(c, sigs...)
}

func signalStop(c chan os.Signal) {
	signal.Stop(c)
}

func sigwinch() os.Signal { return syscall.SIGWINCH }

func sigterm() os.Signal { return syscall.SIGTERM }
