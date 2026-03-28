package domain

import "time"

type PortBinding struct {
	SandboxID     string
	TargetPort    int
	PublishedPort int
	HostAddress   string
	CreatedAt     time.Time
}
