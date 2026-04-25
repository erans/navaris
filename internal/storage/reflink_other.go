//go:build !linux

package storage

import (
	"context"
	"errors"
	"fmt"
)

type ReflinkBackend struct{}

func (ReflinkBackend) Name() string               { return "reflink" }
func (ReflinkBackend) Capabilities() Capabilities { return Capabilities{} }

func (ReflinkBackend) CloneFile(ctx context.Context, src, dst string) error {
	return errors.Join(ErrUnsupported, fmt.Errorf("storage/reflink: only available on Linux"))
}
