package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/navaris/navaris/pkg/client"
	"github.com/spf13/cobra"
)

var sandboxBoostCmd = &cobra.Command{
	Use:   "boost",
	Short: "Time-bounded resource boost (CPU and/or memory)",
}

var (
	boostStartCPU      int
	boostStartMem      int
	boostStartDuration time.Duration
)

var sandboxBoostStartCmd = &cobra.Command{
	Use:   "start <sandbox-id>",
	Short: "Start a boost",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if boostStartCPU == 0 && boostStartMem == 0 {
			return errors.New("at least one of --cpu or --memory is required")
		}
		if boostStartDuration <= 0 {
			return errors.New("--duration must be > 0")
		}
		req := client.StartBoostRequest{DurationSeconds: int(boostStartDuration.Seconds())}
		if boostStartCPU > 0 {
			v := boostStartCPU
			req.CPULimit = &v
		}
		if boostStartMem > 0 {
			v := boostStartMem
			req.MemoryLimitMB = &v
		}
		c := newClient(cmd)
		b, err := c.StartBoost(cmd.Context(), args[0], req)
		if err != nil {
			return err
		}
		fmt.Printf("boost started: %s -> %s (expires %s)\n", b.BoostID, b.SandboxID, b.ExpiresAt)
		return nil
	},
}

var sandboxBoostShowCmd = &cobra.Command{
	Use:   "show <sandbox-id>",
	Short: "Show the active boost (or 404)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient(cmd)
		b, err := c.GetBoost(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		fmt.Printf("boost: %s state=%s expires_at=%s cpu=%s mem=%s\n",
			b.BoostID, b.State, b.ExpiresAt, fmtBoostPtr(b.BoostedCPULimit), fmtBoostPtr(b.BoostedMemoryLimitMB))
		return nil
	},
}

var sandboxBoostCancelCmd = &cobra.Command{
	Use:   "cancel <sandbox-id>",
	Short: "Cancel the active boost (reverts immediately)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient(cmd)
		if err := c.CancelBoost(cmd.Context(), args[0]); err != nil {
			return err
		}
		fmt.Println("boost cancelled")
		return nil
	},
}

func fmtBoostPtr(p *int) string {
	if p == nil {
		return "—"
	}
	return fmt.Sprintf("%d", *p)
}

func init() {
	sandboxBoostStartCmd.Flags().IntVar(&boostStartCPU, "cpu", 0, "boosted CPU limit (0 = leave unchanged)")
	sandboxBoostStartCmd.Flags().IntVar(&boostStartMem, "memory", 0, "boosted memory limit in MB (0 = leave unchanged)")
	sandboxBoostStartCmd.Flags().DurationVar(&boostStartDuration, "duration", 5*time.Minute, "boost duration (e.g. 30s, 5m)")

	sandboxBoostCmd.AddCommand(sandboxBoostStartCmd)
	sandboxBoostCmd.AddCommand(sandboxBoostShowCmd)
	sandboxBoostCmd.AddCommand(sandboxBoostCancelCmd)
}
