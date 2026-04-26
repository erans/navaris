package main

import (
	"fmt"

	"github.com/navaris/navaris/pkg/client"
	"github.com/spf13/cobra"
)

var (
	sandboxResizeCPU int
	sandboxResizeMem int
)

var sandboxResizeCmd = &cobra.Command{
	Use:   "resize <sandbox-id>",
	Short: "Resize CPU and/or memory limits for a sandbox",
	Long: `Update the CPU and/or memory limits for an existing sandbox.

If the sandbox is running, the change is applied live (Incus: cgroup write;
Firecracker: balloon inflate/deflate within the boot-time ceiling). On
Firecracker, CPU live-resize is not yet supported and will fail with HTTP 409;
the limit is still persisted and applied on next start.

At least one of --cpu or --memory must be specified.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if sandboxResizeCPU == 0 && sandboxResizeMem == 0 {
			return fmt.Errorf("at least one of --cpu or --memory is required")
		}
		req := client.UpdateResourcesRequest{}
		if sandboxResizeCPU > 0 {
			v := sandboxResizeCPU
			req.CPULimit = &v
		}
		if sandboxResizeMem > 0 {
			v := sandboxResizeMem
			req.MemoryLimitMB = &v
		}
		c := newClient(cmd)
		resp, err := c.UpdateSandboxResources(cmd.Context(), args[0], req)
		if err != nil {
			return err
		}
		cpuStr := "—"
		memStr := "—"
		if resp.CPULimit != nil {
			cpuStr = fmt.Sprintf("%d", *resp.CPULimit)
		}
		if resp.MemoryLimitMB != nil {
			memStr = fmt.Sprintf("%d", *resp.MemoryLimitMB)
		}
		fmt.Printf("sandbox %s resized: cpu=%s memory=%s applied_live=%v\n",
			resp.SandboxID, cpuStr, memStr, resp.AppliedLive)
		return nil
	},
}

func init() {
	sandboxResizeCmd.Flags().IntVar(&sandboxResizeCPU, "cpu", 0, "new CPU limit (0 = leave unchanged)")
	sandboxResizeCmd.Flags().IntVar(&sandboxResizeMem, "memory", 0, "new memory limit in MB (0 = leave unchanged)")
}
