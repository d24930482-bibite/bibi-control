package ipc

import "errors"

var (
	ErrProtocolUnavailable = errors.New("ipc: protocol unavailable")
	ErrProcessExited       = errors.New("ipc: process exited")
)
