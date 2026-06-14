package ipc

import "errors"

var (
	ErrClosed       = errors.New("ipc: closed")
	ErrNoProcess    = errors.New("ipc: process not started")
	ErrRequestFailed = errors.New("ipc: request failed")
)
