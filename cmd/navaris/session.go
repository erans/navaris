package main

import (
	"fmt"

	"github.com/navaris/navaris/pkg/client"
	"github.com/spf13/cobra"
)

var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Manage interactive sessions",
}

func init() {
	sessionCmd.AddCommand(sessionCreateCmd)
	sessionCmd.AddCommand(sessionListCmd)
	sessionCmd.AddCommand(sessionGetCmd)
	sessionCmd.AddCommand(sessionDestroyCmd)
}

var sessionCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new session attached to a sandbox",
	RunE: func(cmd *cobra.Command, args []string) error {
		sandboxID, _ := cmd.Flags().GetString("sandbox")
		shell, _ := cmd.Flags().GetString("shell")
		backing, _ := cmd.Flags().GetString("backing")

		c := newClient(cmd)
		s, err := c.CreateSession(cmd.Context(), sandboxID, client.CreateSessionRequest{
			Shell:   shell,
			Backing: backing,
		})
		if err != nil {
			return err
		}
		printResult(s, []string{"SESSION_ID", "SANDBOX_ID", "SHELL", "BACKING", "STATE"}, func() [][]string {
			return [][]string{{s.SessionID, s.SandboxID, s.Shell, s.Backing, s.State}}
		})
		return nil
	},
}

var sessionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List sessions for a sandbox",
	RunE: func(cmd *cobra.Command, args []string) error {
		sandboxID, _ := cmd.Flags().GetString("sandbox")

		c := newClient(cmd)
		sessions, err := c.ListSessions(cmd.Context(), sandboxID)
		if err != nil {
			return err
		}
		printResult(sessions, []string{"SESSION_ID", "SANDBOX_ID", "SHELL", "BACKING", "STATE", "CREATED_AT"}, func() [][]string {
			rows := make([][]string, len(sessions))
			for i, s := range sessions {
				rows[i] = []string{
					s.SessionID, s.SandboxID, s.Shell, s.Backing, s.State,
					s.CreatedAt.Format("2006-01-02T15:04:05Z"),
				}
			}
			return rows
		})
		return nil
	},
}

var sessionGetCmd = &cobra.Command{
	Use:   "get <session-id>",
	Short: "Get a session by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient(cmd)
		s, err := c.GetSession(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		printResult(s, []string{"SESSION_ID", "SANDBOX_ID", "SHELL", "BACKING", "STATE", "CREATED_AT"}, func() [][]string {
			return [][]string{{
				s.SessionID, s.SandboxID, s.Shell, s.Backing, s.State,
				s.CreatedAt.Format("2006-01-02T15:04:05Z"),
			}}
		})
		return nil
	},
}

var sessionDestroyCmd = &cobra.Command{
	Use:   "destroy <session-id>",
	Short: "Destroy a session",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient(cmd)
		if err := c.DestroySession(cmd.Context(), args[0]); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Session %s destroyed\n", args[0])
		return nil
	},
}

func init() {
	sessionCreateCmd.Flags().String("sandbox", "", "Sandbox ID (required)")
	_ = sessionCreateCmd.MarkFlagRequired("sandbox")
	sessionCreateCmd.Flags().String("shell", "bash", "Shell to use")
	sessionCreateCmd.Flags().String("backing", "direct", "Session backing type")

	sessionListCmd.Flags().String("sandbox", "", "Sandbox ID (required)")
	_ = sessionListCmd.MarkFlagRequired("sandbox")
}
