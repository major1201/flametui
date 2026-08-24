//go:build !windows

package term

import (
	"fmt"
	"syscall"
	"unsafe"
)

// State holds the terminal state.
type State struct {
	termios syscall.Termios
}

// MakeRaw puts the terminal into raw mode.
func MakeRaw(fd int) (*State, error) {
	var oldState State
	if _, _, err := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), ioctlReadTermios, uintptr(unsafe.Pointer(&oldState.termios)), 0, 0, 0); err != 0 {
		return nil, fmt.Errorf("ioctl read termios: %v", err)
	}

	newState := oldState.termios
	newState.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	newState.Oflag &^= syscall.OPOST
	newState.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	newState.Cflag &^= syscall.CSIZE | syscall.PARENB
	newState.Cflag |= syscall.CS8
	newState.Cc[syscall.VMIN] = 1
	newState.Cc[syscall.VTIME] = 0

	if _, _, err := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), ioctlWriteTermios, uintptr(unsafe.Pointer(&newState)), 0, 0, 0); err != 0 {
		return nil, fmt.Errorf("ioctl write termios: %v", err)
	}

	return &oldState, nil
}

// Restore restores the terminal to its previous state.
func Restore(fd int, state *State) error {
	if state == nil {
		return nil
	}
	if _, _, err := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), ioctlWriteTermios, uintptr(unsafe.Pointer(&state.termios)), 0, 0, 0); err != 0 {
		return fmt.Errorf("ioctl write termios: %v", err)
	}
	return nil
}

// GetSize returns the terminal size.
func GetSize(fd int) (int, int, error) {
	var ws winsize
	if _, _, err := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws))); err != 0 {
		return 0, 0, fmt.Errorf("ioctl TIOCGWINSZ: %v", err)
	}
	return int(ws.Col), int(ws.Row), nil
}

type winsize struct {
	Row    uint16
	Col    uint16
	Xpixel uint16
	Ypixel uint16
}

// IsTerminal returns true if fd is a terminal.
func IsTerminal(fd int) bool {
	var termios syscall.Termios
	_, _, err := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), ioctlReadTermios, uintptr(unsafe.Pointer(&termios)), 0, 0, 0)
	return err == 0
}
