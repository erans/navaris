//go:build firecracker

package firecracker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/telemetry"
)

// validateRef rejects refs containing path separators or traversal.
func validateRef(ref string) error {
	if strings.ContainsAny(ref, "/\\") || strings.Contains(ref, "..") || ref == "" {
		return fmt.Errorf("invalid ref %q", ref)
	}
	return nil
}

type imageInfo struct {
	Ref            string `json:"ref"`
	Name           string `json:"name"`
	Version        string `json:"version"`
	Architecture   string `json:"architecture"`
	Size           int64  `json:"size"`
	SourceSnapshot string `json:"source_snapshot"`
	StorageBackend string `json:"storage_backend,omitempty"`
}

func (p *Provider) imageExtPath(ref string) string {
	return filepath.Join(p.config.ImageDir, ref+".ext4")
}

func (p *Provider) imageMetaPath(ref string) string {
	return filepath.Join(p.config.ImageDir, ref+".json")
}

func (p *Provider) PublishSnapshotAsImage(ctx context.Context, snapshotRef domain.BackendRef, req domain.PublishImageRequest) (_ domain.BackendRef, retErr error) {
	ctx, endSpan := telemetry.ProviderSpan(ctx, backendName, "PublishSnapshotAsImage")
	defer func() { endSpan(retErr) }()

	snapID := snapshotRef.Ref
	if err := validateRef(snapID); err != nil {
		return domain.BackendRef{}, fmt.Errorf("firecracker publish image: %w", err)
	}
	snapDir := p.snapshotDir(snapID)

	imgRef := "img-" + uuid.NewString()[:8]

	// Copy rootfs from snapshot to image directory.
	src := filepath.Join(snapDir, "rootfs.ext4")
	dst := p.imageExtPath(imgRef)
	b, err := p.cloneFile(ctx, src, dst)
	if err != nil {
		return domain.BackendRef{}, fmt.Errorf("firecracker publish image copy: %w", err)
	}

	backendName := ""
	if b != nil {
		backendName = b.Name()
		slog.Debug("firecracker image clone", "image_ref", imgRef, "snap_id", snapID, "backend", backendName)
	}

	// Get file size.
	fi, err := os.Stat(dst)
	if err != nil {
		os.Remove(dst)
		return domain.BackendRef{}, fmt.Errorf("firecracker publish image stat: %w", err)
	}

	// Write metadata.
	meta := &imageInfo{
		Ref:            imgRef,
		Name:           req.Name,
		Version:        req.Version,
		Architecture:   runtime.GOARCH,
		Size:           fi.Size(),
		SourceSnapshot: snapID,
		StorageBackend: backendName,
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		os.Remove(dst)
		return domain.BackendRef{}, fmt.Errorf("firecracker publish image marshal: %w", err)
	}
	if err := os.WriteFile(p.imageMetaPath(imgRef), data, 0o644); err != nil {
		os.Remove(dst)
		return domain.BackendRef{}, fmt.Errorf("firecracker publish image write meta: %w", err)
	}

	return domain.BackendRef{Backend: backendName, Ref: imgRef}, nil
}

func (p *Provider) GetImageInfo(ctx context.Context, imageRef domain.BackendRef) (_ domain.ImageInfo, retErr error) {
	ctx, endSpan := telemetry.ProviderSpan(ctx, backendName, "GetImageInfo")
	defer func() { endSpan(retErr) }()

	if err := validateRef(imageRef.Ref); err != nil {
		return domain.ImageInfo{}, fmt.Errorf("firecracker get image info: %w", err)
	}
	data, err := os.ReadFile(p.imageMetaPath(imageRef.Ref))
	if err != nil {
		return domain.ImageInfo{}, fmt.Errorf("firecracker get image info %s: %w", imageRef.Ref, err)
	}
	var meta imageInfo
	if err := json.Unmarshal(data, &meta); err != nil {
		return domain.ImageInfo{}, fmt.Errorf("firecracker parse image info %s: %w", imageRef.Ref, err)
	}
	return domain.ImageInfo{
		Architecture: meta.Architecture,
		Size:         meta.Size,
	}, nil
}

func (p *Provider) DeleteImage(ctx context.Context, imageRef domain.BackendRef) (retErr error) {
	ctx, endSpan := telemetry.ProviderSpan(ctx, backendName, "DeleteImage")
	defer func() { endSpan(retErr) }()

	ref := imageRef.Ref
	if err := validateRef(ref); err != nil {
		return fmt.Errorf("firecracker delete image: %w", err)
	}
	var errs []error
	if err := os.Remove(p.imageExtPath(ref)); err != nil && !os.IsNotExist(err) {
		errs = append(errs, err)
	}
	if err := os.Remove(p.imageMetaPath(ref)); err != nil && !os.IsNotExist(err) {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
