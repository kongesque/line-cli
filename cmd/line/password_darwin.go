package main

import "golang.org/x/sys/unix"

const terminalReadRequest = unix.TIOCGETA
const terminalWriteRequest = unix.TIOCSETA
