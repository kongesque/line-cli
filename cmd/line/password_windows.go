package main

import "golang.org/x/sys/windows"

func disableEcho(fd int) error {
	var mode uint32
	if err := windows.GetConsoleMode(windows.Handle(fd), &mode); err != nil {
		return err
	}
	mode &^= windows.ENABLE_ECHO_INPUT
	mode |= windows.ENABLE_LINE_INPUT | windows.ENABLE_PROCESSED_INPUT
	return windows.SetConsoleMode(windows.Handle(fd), mode)
}
