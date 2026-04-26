//go:build incus

package incus

import (
	"context"
	"fmt"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/telemetry"
)

// UpdateResources applies new CPU and/or memory limits to a running Incus
// container by issuing a PATCH-equivalent UpdateInstance with the new
// limits in instance.Config. The cgroup writes happen live; no restart
// required.
func (p *IncusProvider) UpdateResources(ctx context.Context, ref domain.BackendRef, req domain.UpdateResourcesRequest) (retErr error) {
	ctx, endSpan := telemetry.ProviderSpan(ctx, backendName, "UpdateResources")
	defer func() { endSpan(retErr) }()

	if req.CPULimit == nil && req.MemoryLimitMB == nil {
		return nil
	}

	inst, etag, err := p.client.GetInstance(ref.Ref)
	if err != nil {
		return fmt.Errorf("incus get instance %q: %w", ref.Ref, err)
	}
	put := inst.Writable()
	if put.Config == nil {
		put.Config = map[string]string{}
	}
	if req.CPULimit != nil {
		put.Config["limits.cpu"] = fmt.Sprintf("%d", *req.CPULimit)
	}
	if req.MemoryLimitMB != nil {
		put.Config["limits.memory"] = fmt.Sprintf("%dMB", *req.MemoryLimitMB)
	}
	op, err := p.client.UpdateInstance(ref.Ref, put, etag)
	if err != nil {
		return fmt.Errorf("incus update instance %q: %w", ref.Ref, err)
	}
	if err := op.WaitContext(ctx); err != nil {
		return fmt.Errorf("incus update instance wait %q: %w", ref.Ref, err)
	}
	return nil
}
