//go:build firecracker

package firecracker

import (
	"testing"

	"github.com/navaris/navaris/internal/domain"
)

func TestResolveMachineLimits_AppliesHeadroom(t *testing.T) {
	cases := []struct {
		name           string
		vcpuMult       float64
		memMult        float64
		req            domain.CreateSandboxRequest
		wantLimitCPU   int64
		wantLimitMem   int64
		wantCeilingCPU int64
		wantCeilingMem int64
	}{
		{
			name:           "default 2x headroom",
			vcpuMult:       2.0,
			memMult:        2.0,
			req:            domain.CreateSandboxRequest{CPULimit: ptrIntFC(2), MemoryLimitMB: ptrIntFC(256)},
			wantLimitCPU:   2,
			wantLimitMem:   256,
			wantCeilingCPU: 4,
			wantCeilingMem: 512,
		},
		{
			name:           "1x headroom = no headroom",
			vcpuMult:       1.0,
			memMult:        1.0,
			req:            domain.CreateSandboxRequest{CPULimit: ptrIntFC(2), MemoryLimitMB: ptrIntFC(256)},
			wantLimitCPU:   2,
			wantLimitMem:   256,
			wantCeilingCPU: 2,
			wantCeilingMem: 256,
		},
		{
			name:           "ceiling clamped to FC max (32 vcpu, 8192 mem)",
			vcpuMult:       4.0,
			memMult:        4.0,
			req:            domain.CreateSandboxRequest{CPULimit: ptrIntFC(16), MemoryLimitMB: ptrIntFC(4096)},
			wantLimitCPU:   16,
			wantLimitMem:   4096,
			wantCeilingCPU: 32,
			wantCeilingMem: 8192,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &Provider{config: Config{
				DefaultVcpuCount: 1, DefaultMemoryMib: 256,
				VcpuHeadroomMult: tc.vcpuMult, MemHeadroomMult: tc.memMult,
			}}
			got := p.resolveMachineLimits(tc.req)
			if got.LimitCPU != tc.wantLimitCPU || got.LimitMemMib != tc.wantLimitMem || got.CeilingCPU != tc.wantCeilingCPU || got.CeilingMemMib != tc.wantCeilingMem {
				t.Fatalf("got %+v, want LimitCPU=%d LimitMem=%d CeilingCPU=%d CeilingMem=%d",
					got, tc.wantLimitCPU, tc.wantLimitMem, tc.wantCeilingCPU, tc.wantCeilingMem)
			}
		})
	}
}

func ptrIntFC(v int) *int { return &v }
