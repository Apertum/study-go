//go:build !windows

package main

import (
	"errors"
	"syscall"
)

// killPid доставляет сигнал процессу. На POSIX-платформах это настоящий syscall.Kill —
// именно его поведение (а не отмену контекста) проверяют сквозные тесты остановки.
func killPid(pid int, sig syscall.Signal) error {
	return syscall.Kill(pid, sig)
}

// procGone отвечает true, когда сигнал некому доставлять: процесс уже завершился.
func procGone(err error) bool {
	return errors.Is(err, syscall.ESRCH)
}
