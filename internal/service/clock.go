package service

import "time"

// Clock is a tiny abstraction over the real clock so tests that exercise
// timer behavior don't need to sleep. Production code uses RealClock.
type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, fn func()) Timer
}

// Timer is the subset of *time.Timer that BoostService uses.
type Timer interface {
	Stop() bool
}

type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }
func (RealClock) AfterFunc(d time.Duration, fn func()) Timer {
	return time.AfterFunc(d, fn)
}
