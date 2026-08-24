//go:build windows

package tui

// On Windows, resize events are delivered via the console input buffer.
// For simplicity, we skip automatic resize handling.
func (a *App) handleResize() {}