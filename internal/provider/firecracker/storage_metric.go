//go:build firecracker

package firecracker

import (
	"context"
	"path/filepath"
	"time"

	"github.com/navaris/navaris/internal/storage"
	"github.com/navaris/navaris/internal/telemetry"
)

// cloneFile wraps p.storage.CloneFile with timing-and-record. The src's
// parent directory is used as the source_root attribute so emission
// distinguishes vmDir/snapshotDir/imageDir clones in dashboards.
func (p *Provider) cloneFile(ctx context.Context, src, dst string) (storage.Backend, error) {
	start := time.Now()
	b, err := p.storage.CloneFile(ctx, src, dst)
	d := time.Since(start)
	backendName := "unknown"
	if b != nil {
		backendName = b.Name()
	}
	telemetry.RecordStorageCloneDuration(ctx, backendName, filepath.Dir(src), d)
	return b, err
}
