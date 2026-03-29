//go:build incus

package incus

import (
	"context"
	"fmt"
	"strings"

	incusapi "github.com/lxc/incus/v6/shared/api"
	"github.com/navaris/navaris/internal/domain"
)

// PublishSnapshotAsImage publishes a container snapshot as a reusable Incus
// image. The snapshotRef.Ref is expected to be "container/snapshot".
func (p *IncusProvider) PublishSnapshotAsImage(ctx context.Context, snapshotRef domain.BackendRef, req domain.PublishImageRequest) (domain.BackendRef, error) {
	parts := strings.SplitN(snapshotRef.Ref, "/", 2)
	if len(parts) != 2 {
		return domain.BackendRef{}, fmt.Errorf("invalid snapshot ref %q: expected container/snapshot", snapshotRef.Ref)
	}

	alias := req.Name
	if req.Version != "" {
		alias = req.Name + ":" + req.Version
	}

	publishReq := incusapi.ImagesPost{
		Source: &incusapi.ImagesPostSource{
			Type: "snapshot",
			Name: snapshotRef.Ref, // "container/snapshot"
		},
	}

	op, err := p.client.CreateImage(publishReq, nil)
	if err != nil {
		return domain.BackendRef{}, fmt.Errorf("incus publish image from %s: %w", snapshotRef.Ref, err)
	}
	if err := op.WaitContext(ctx); err != nil {
		return domain.BackendRef{}, fmt.Errorf("incus publish image wait: %w", err)
	}

	// Extract the fingerprint from the operation metadata.
	opAPI := op.Get()
	fingerprint, ok := opAPI.Metadata["fingerprint"].(string)
	if !ok || fingerprint == "" {
		return domain.BackendRef{}, fmt.Errorf("incus publish image: missing fingerprint in response")
	}

	// Create an alias for the image so it can be addressed by name.
	aliasPost := incusapi.ImageAliasesPost{
		ImageAliasesEntry: incusapi.ImageAliasesEntry{
			Name: alias,
			ImageAliasesEntryPut: incusapi.ImageAliasesEntryPut{
				Description: fmt.Sprintf("navaris image %s", alias),
				Target:      fingerprint,
			},
		},
	}
	if err := p.client.CreateImageAlias(aliasPost); err != nil {
		// Alias creation is best-effort; the fingerprint ref still works.
		_ = err
	}

	return domain.BackendRef{Backend: backendName, Ref: fingerprint}, nil
}

// DeleteImage removes an image from the Incus image store. The imageRef.Ref
// is the image fingerprint.
func (p *IncusProvider) DeleteImage(ctx context.Context, imageRef domain.BackendRef) error {
	op, err := p.client.DeleteImage(imageRef.Ref)
	if err != nil {
		return fmt.Errorf("incus delete image %s: %w", imageRef.Ref, err)
	}
	return op.WaitContext(ctx)
}

// GetImageInfo retrieves metadata for an image by its fingerprint.
func (p *IncusProvider) GetImageInfo(ctx context.Context, imageRef domain.BackendRef) (domain.ImageInfo, error) {
	img, _, err := p.client.GetImage(imageRef.Ref)
	if err != nil {
		return domain.ImageInfo{}, fmt.Errorf("incus get image %s: %w", imageRef.Ref, err)
	}

	return domain.ImageInfo{
		Architecture: img.Architecture,
		Size:         img.Size,
	}, nil
}
