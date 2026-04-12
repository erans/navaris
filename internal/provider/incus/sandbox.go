//go:build incus

package incus

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	incusclient "github.com/lxc/incus/v6/client"
	incusapi "github.com/lxc/incus/v6/shared/api"
	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/telemetry"
)

// containerName generates a deterministic container name from a UUID.
func containerName() string {
	return "nvrs-" + uuid.NewString()[:8]
}

// CreateSandbox creates a new Incus container instance from the given image
// reference, applies resource limits, and returns a BackendRef.
func (p *IncusProvider) CreateSandbox(ctx context.Context, req domain.CreateSandboxRequest) (_ domain.BackendRef, retErr error) {
	ctx, endSpan := telemetry.ProviderSpan(ctx, backendName, "CreateSandbox")
	defer func() { endSpan(retErr) }()

	name := containerName()

	instanceCfg := map[string]string{}
	if req.CPULimit != nil {
		instanceCfg["limits.cpu"] = fmt.Sprintf("%d", *req.CPULimit)
	}
	if req.MemoryLimitMB != nil {
		instanceCfg["limits.memory"] = fmt.Sprintf("%dMB", *req.MemoryLimitMB)
	}

	instanceReq := incusapi.InstancesPost{
		Name: name,
		Source: incusapi.InstanceSource{
			Type:  "image",
			Alias: req.ImageRef,
		},
		InstancePut: incusapi.InstancePut{
			Config: instanceCfg,
		},
		Type: incusapi.InstanceTypeContainer,
	}

	op, err := p.client.CreateInstance(instanceReq)
	if err != nil {
		return domain.BackendRef{}, fmt.Errorf("incus create instance: %w", err)
	}
	if err := op.WaitContext(ctx); err != nil {
		return domain.BackendRef{}, fmt.Errorf("incus create instance wait: %w", err)
	}

	return domain.BackendRef{Backend: backendName, Ref: name}, nil
}

// StartSandbox starts a stopped or newly created container.
func (p *IncusProvider) StartSandbox(ctx context.Context, ref domain.BackendRef) (retErr error) {
	ctx, endSpan := telemetry.ProviderSpan(ctx, backendName, "StartSandbox")
	defer func() { endSpan(retErr) }()

	// Idempotency: if Incus already considers the instance running, return
	// success without issuing another start. This matters after a navarisd
	// restart — the Incus daemon auto-starts instances that were running
	// before it went down, and if reconciliation observes the instance
	// mid-restart (stopped) and later a user hits Start, Incus would
	// otherwise return "The instance is already running" and handleStart
	// would treat that as fatal, flipping a healthy sandbox to failed.
	// A failed GetInstanceState is swallowed so a transient query error
	// doesn't block a legitimate start; the real start call below will
	// surface any persistent connectivity issue.
	if state, _, err := p.client.GetInstanceState(ref.Ref); err == nil {
		if mapIncusStatus(state.Status) == domain.SandboxRunning {
			return nil
		}
	}

	reqState := incusapi.InstanceStatePut{
		Action:  "start",
		Timeout: -1,
	}

	op, err := p.client.UpdateInstanceState(ref.Ref, reqState, "")
	if err != nil {
		return fmt.Errorf("incus start %s: %w", ref.Ref, err)
	}
	return op.WaitContext(ctx)
}

// StopSandbox stops a running container. If force is true the container is
// killed immediately rather than given a graceful shutdown period.
func (p *IncusProvider) StopSandbox(ctx context.Context, ref domain.BackendRef, force bool) (retErr error) {
	ctx, endSpan := telemetry.ProviderSpan(ctx, backendName, "StopSandbox")
	defer func() { endSpan(retErr) }()

	// Idempotency: mirror StartSandbox. If Incus already considers the
	// instance stopped, there's nothing to do and we must not let Incus'
	// "The instance is already stopped" error propagate as fatal.
	if state, _, err := p.client.GetInstanceState(ref.Ref); err == nil {
		if mapIncusStatus(state.Status) == domain.SandboxStopped {
			return nil
		}
	}

	reqState := incusapi.InstanceStatePut{
		Action:  "stop",
		Timeout: -1,
		Force:   force,
	}

	op, err := p.client.UpdateInstanceState(ref.Ref, reqState, "")
	if err != nil {
		return fmt.Errorf("incus stop %s: %w", ref.Ref, err)
	}
	return op.WaitContext(ctx)
}

// DestroySandbox deletes the container and all its snapshots.
func (p *IncusProvider) DestroySandbox(ctx context.Context, ref domain.BackendRef) (retErr error) {
	ctx, endSpan := telemetry.ProviderSpan(ctx, backendName, "DestroySandbox")
	defer func() { endSpan(retErr) }()

	op, err := p.client.DeleteInstance(ref.Ref)
	if err != nil {
		return fmt.Errorf("incus delete %s: %w", ref.Ref, err)
	}
	return op.WaitContext(ctx)
}

// GetSandboxState queries Incus for the current container status and maps it
// to a domain.SandboxState.
func (p *IncusProvider) GetSandboxState(ctx context.Context, ref domain.BackendRef) (_ domain.SandboxState, retErr error) {
	ctx, endSpan := telemetry.ProviderSpan(ctx, backendName, "GetSandboxState")
	defer func() { endSpan(retErr) }()

	state, _, err := p.client.GetInstanceState(ref.Ref)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return domain.SandboxDestroyed, nil
		}
		return "", fmt.Errorf("incus state %s: %w", ref.Ref, err)
	}

	return mapIncusStatus(state.Status), nil
}

// mapIncusStatus converts an Incus status string to a domain sandbox state.
func mapIncusStatus(status string) domain.SandboxState {
	switch strings.ToUpper(status) {
	case "RUNNING":
		return domain.SandboxRunning
	case "STOPPED", "STOP":
		return domain.SandboxStopped
	case "STARTING":
		return domain.SandboxStarting
	case "STOPPING":
		return domain.SandboxStopping
	case "ERROR", "BROKEN":
		return domain.SandboxFailed
	case "FROZEN", "FREEZING", "THAWED":
		return domain.SandboxRunning
	default:
		return domain.SandboxPending
	}
}

// CreateSandboxFromSnapshot creates a new container by copying the specified
// snapshot. The snapshotRef.Ref is expected to be "container/snapshot".
func (p *IncusProvider) CreateSandboxFromSnapshot(ctx context.Context, snapshotRef domain.BackendRef, req domain.CreateSandboxRequest) (_ domain.BackendRef, retErr error) {
	ctx, endSpan := telemetry.ProviderSpan(ctx, backendName, "CreateSandboxFromSnapshot")
	defer func() { endSpan(retErr) }()

	name := containerName()

	// Parse "container/snapshot" from the snapshotRef.
	parts := strings.SplitN(snapshotRef.Ref, "/", 2)
	if len(parts) != 2 {
		return domain.BackendRef{}, fmt.Errorf("invalid snapshot ref %q: expected container/snapshot", snapshotRef.Ref)
	}
	srcContainer := parts[0]
	srcSnapshot := parts[1]

	// Get the source snapshot for copy.
	snapshot, _, err := p.client.GetInstanceSnapshot(srcContainer, srcSnapshot)
	if err != nil {
		return domain.BackendRef{}, fmt.Errorf("incus get snapshot %s: %w", snapshotRef.Ref, err)
	}

	// Build config overrides for resource limits.
	instanceCfg := map[string]string{}
	if req.CPULimit != nil {
		instanceCfg["limits.cpu"] = fmt.Sprintf("%d", *req.CPULimit)
	}
	if req.MemoryLimitMB != nil {
		instanceCfg["limits.memory"] = fmt.Sprintf("%dMB", *req.MemoryLimitMB)
	}

	copyArgs := incusclient.InstanceSnapshotCopyArgs{
		Name: name,
	}

	op, err := p.client.CopyInstanceSnapshot(p.client, srcContainer, *snapshot, &copyArgs)
	if err != nil {
		return domain.BackendRef{}, fmt.Errorf("incus copy snapshot: %w", err)
	}
	// RemoteOperation only exposes Wait() (no WaitContext). Wrap it so
	// context cancellation still propagates and we attempt to cancel the
	// backend operation when the caller gives up.
	waitCh := make(chan error, 1)
	go func() { waitCh <- op.Wait() }()
	select {
	case err := <-waitCh:
		if err != nil {
			return domain.BackendRef{}, fmt.Errorf("incus copy snapshot wait: %w", err)
		}
	case <-ctx.Done():
		_ = op.CancelTarget()
		return domain.BackendRef{}, fmt.Errorf("incus copy snapshot wait: %w", ctx.Err())
	}

	// Apply resource limits after copy if any were specified.
	// Also clear volatile NIC keys so Incus assigns a fresh MAC address —
	// without this the copied instance keeps the source's MAC and Incus
	// rejects the duplicate on start.
	{
		inst, etag, err := p.client.GetInstance(name)
		if err != nil {
			return domain.BackendRef{}, fmt.Errorf("incus get copied instance: %w", err)
		}
		for k, v := range instanceCfg {
			inst.Config[k] = v
		}
		// Remove volatile.*.hwaddr keys that carry the source's MAC.
		for k := range inst.Config {
			if strings.HasPrefix(k, "volatile.") && strings.HasSuffix(k, ".hwaddr") {
				delete(inst.Config, k)
			}
		}
		op, err := p.client.UpdateInstance(name, inst.Writable(), etag)
		if err != nil {
			return domain.BackendRef{}, fmt.Errorf("incus update copied instance config: %w", err)
		}
		if err := op.WaitContext(ctx); err != nil {
			return domain.BackendRef{}, fmt.Errorf("incus update copied instance wait: %w", err)
		}
	}

	return domain.BackendRef{Backend: backendName, Ref: name}, nil
}
