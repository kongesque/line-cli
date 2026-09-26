//go:build darwin || linux

package main

import "golang.org/x/sys/unix"

func disableEcho(fd int) error {
	state, err := unix.IoctlGetTermios(fd, terminalReadRequest)
	if err != nil {
		return err
	}
	state.Lflag &^= unix.ECHO | unix.ECHONL
	state.Lflag |= unix.ICANON | unix.ISIG
	state.Iflag |= unix.ICRNL
	return unix.IoctlSetTermios(fd, terminalWriteRequest, state)
}
