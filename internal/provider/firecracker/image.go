//go:build firecracker

package firecracker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
)

type imageInfo struct {
	Ref            string `json:"ref"`
	Name           string `json:"name"`
	Version        string `json:"version"`
	Architecture   string `json:"architecture"`
	Size           int64  `json:"size"`
	SourceSnapshot string `json:"source_snapshot"`
}

func (p *Provider) imageExtPath(ref string) string {
	return filepath.Join(p.config.ImageDir, ref+".ext4")
}

func (p *Provider) imageMetaPath(ref string) string {
	return filepath.Join(p.config.ImageDir, ref+".json")
}

func (p *Provider) PublishSnapshotAsImage(ctx context.Context, snapshotRef domain.BackendRef, req domain.PublishImageRequest) (domain.BackendRef, error) {
	snapID := snapshotRef.Ref
	snapDir := p.snapshotDir(snapID)

	imgRef := "img-" + uuid.NewString()[:8]

	// Copy rootfs from snapshot to image directory.
	src := filepath.Join(snapDir, "rootfs.ext4")
	dst := p.imageExtPath(imgRef)
	if err := copyFile(src, dst); err != nil {
		return domain.BackendRef{}, fmt.Errorf("firecracker publish image copy: %w", err)
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

func (p *Provider) GetImageInfo(ctx context.Context, imageRef domain.BackendRef) (domain.ImageInfo, error) {
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

func (p *Provider) DeleteImage(ctx context.Context, imageRef domain.BackendRef) error {
	ref := imageRef.Ref
	os.Remove(p.imageExtPath(ref))
	os.Remove(p.imageMetaPath(ref))
	return nil
}
