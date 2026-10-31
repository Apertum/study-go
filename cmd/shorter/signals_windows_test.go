//go:build windows

package main

import "syscall"

// killPid на Windows не может послать сигнал чужому процессу: syscall.Kill здесь нет, а
// Ctrl+C доставляет только SIGINT да и тот требует прикреплённой консоли. Сквозные проверки
// сигналов пропускаются на этой платформе (см. shorterProcess.stopBySignal).
func killPid(_ int, _ syscall.Signal) error {
	return errSignalsUnsupported
}

// procGone на Windows недоступна вместе с доставкой сигналов.
func procGone(_ error) bool {
	return false
}
