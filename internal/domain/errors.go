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
)
