package domain

import "time"

type Project struct {
	ProjectID string
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
	Metadata  map[string]any
}
