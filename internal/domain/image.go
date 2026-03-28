package domain

import "time"

type ImageState string

const (
	ImagePending ImageState = "pending"
	ImageReady   ImageState = "ready"
	ImageFailed  ImageState = "failed"
	ImageDeleted ImageState = "deleted"
)

func (s ImageState) Valid() bool {
	switch s {
	case ImagePending, ImageReady, ImageFailed, ImageDeleted:
		return true
	}
	return false
}

type SourceType string

const (
	SourceImported         SourceType = "imported"
	SourceSnapshotPromoted SourceType = "snapshot_promoted"
)

type BaseImage struct {
	ImageID          string
	ProjectScope     string
	Name             string
	Version          string
	SourceType       SourceType
	SourceSnapshotID string
	Backend          string
	BackendRef       string
	Architecture     string
	State            ImageState
	CreatedAt        time.Time
	Metadata         map[string]any
}

type ImageFilter struct {
	Name         *string
	Architecture *string
	State        *ImageState
}
