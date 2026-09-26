package main

import "golang.org/x/sys/unix"

const terminalReadRequest = unix.TCGETS
const terminalWriteRequest = unix.TCSETS
