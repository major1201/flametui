//go:build !windows

package tui

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/major1201/flametui/pkg/term"
)

func (a *App) handleResize() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	go func() {
		for range sigCh {
			w, h, err := term.GetSize(int(os.Stdout.Fd()))
			if err == nil {
				a.width = w
				a.height = h
			}
			a.render()
		}
	}()
}