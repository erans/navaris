package domain

import "errors"

var (
	ErrNotFound         = errors.New("not found")
	ErrConflict         = errors.New("conflict")
	ErrInvalidState     = errors.New("invalid state transition")
	ErrCapacityExceeded = errors.New("capacity exceeded")
	ErrUnauthorized     = errors.New("unauthorized")
	ErrForbidden        = errors.New("forbidden")
	ErrBusy             = errors.New("database busy")
	ErrNotSupported     = errors.New("operation not supported")
	ErrInvalidArgument  = errors.New("invalid argument")
	ErrVMStopped        = errors.New("vm is stopping") // F1: returned by Provider.lockFor when the VM is mid-teardown
)
