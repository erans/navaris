package domain

import "time"

type EventType string

const (
	EventSandboxStateChanged     EventType = "sandbox_state_changed"
	EventSandboxResourcesUpdated EventType = "sandbox_resources_updated"
	EventSnapshotStateChanged    EventType = "snapshot_state_changed"
	EventImageStateChanged       EventType = "image_state_changed"
	EventSessionStateChanged     EventType = "session_state_changed"
	EventOperationStateChanged   EventType = "operation_state_changed"
	EventExecOutput              EventType = "exec_output"
	EventExecCompleted           EventType = "exec_completed"
	EventUILogin                 EventType = "ui.login"
	EventUILoginFailed           EventType = "ui.login_failed"
	EventUIAttachOpened          EventType = "ui.attach_opened"
	EventUIAttachClosed          EventType = "ui.attach_closed"
)

type Event struct {
	Type      EventType
	Timestamp time.Time
	Data      map[string]any
}

type EventFilter struct {
	ProjectID *string
	SandboxID *string
	Types     []EventType
}
