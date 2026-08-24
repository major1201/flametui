//go:build windows

package term

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleMode   = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode   = kernel32.NewProc("SetConsoleMode")
	procGetConsoleScreenBufferInfo = kernel32.NewProc("GetConsoleScreenBufferInfo")
)

const (
	enableProcessedInput            = 0x0001
	enableLineInput                 = 0x0002
	enableEchoInput                 = 0x0004
	enableMouseInput                = 0x0010
	enableWindowInput               = 0x0008
	enableVirtualTerminalProcessing = 0x0004
	enableExtendedFlags             = 0x0080

	disableNewlineAutoReturn = 0x0008
)

// State holds the terminal state (console mode).
type State struct {
	mode uint32
}

// MakeRaw puts the terminal into raw mode.
func MakeRaw(fd int) (*State, error) {
	var oldMode uint32
	ret, _, err := procGetConsoleMode.Call(uintptr(fd), uintptr(unsafe.Pointer(&oldMode)))
	if ret == 0 {
		return nil, fmt.Errorf("GetConsoleMode: %v", err)
	}

	newMode := oldMode
	// Disable these input modes
	newMode &^= enableProcessedInput | enableLineInput | enableEchoInput
	// Enable virtual terminal processing (ANSI escape sequences) and mouse input
	newMode |= enableVirtualTerminalProcessing | enableExtendedFlags | enableMouseInput | enableWindowInput

	ret, _, err = procSetConsoleMode.Call(uintptr(fd), uintptr(newMode))
	if ret == 0 {
		return nil, fmt.Errorf("SetConsoleMode: %v", err)
	}

	return &State{mode: oldMode}, nil
}

// Restore restores the terminal to its previous state.
func Restore(fd int, state *State) error {
	if state == nil {
		return nil
	}
	ret, _, err := procSetConsoleMode.Call(uintptr(fd), uintptr(state.mode))
	if ret == 0 {
		return fmt.Errorf("SetConsoleMode: %v", err)
	}
	return nil
}

type winsize struct {
	Row    uint16
	Col    uint16
	Xpixel uint16
	Ypixel uint16
}

type consoleScreenBufferInfo struct {
	Size       winsize
	CursorPos  struct{ X, Y int16 }
	Attrs      uint16
	Window     struct{ Left, Top, Right, Bottom int16 }
	MaxWinSize winsize
}

// GetSize returns the terminal size.
func GetSize(fd int) (int, int, error) {
	var info consoleScreenBufferInfo
	ret, _, err := procGetConsoleScreenBufferInfo.Call(uintptr(fd), uintptr(unsafe.Pointer(&info)))
	if ret == 0 {
		return 0, 0, fmt.Errorf("GetConsoleScreenBufferInfo: %v", err)
	}
	w := int(info.Window.Right - info.Window.Left + 1)
	h := int(info.Window.Bottom - info.Window.Top + 1)
	return w, h, nil
}

// IsTerminal returns true if fd is a terminal.
func IsTerminal(fd int) bool {
	var mode uint32
	ret, _, _ := procGetConsoleMode.Call(uintptr(fd), uintptr(unsafe.Pointer(&mode)))
	return ret != 0
}