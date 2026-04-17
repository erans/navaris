// Package main implements the navaris CLI for the sandbox control plane.
package main

import (
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "navaris",
	Short: "Navaris sandbox control plane CLI",
	Long:  "Command-line interface for managing sandboxes, snapshots, images, and sessions.",
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.PersistentFlags().String("api-url", "", "API base URL (env: NAVARIS_API_URL)")
	rootCmd.PersistentFlags().String("token", "", "Authentication token (env: NAVARIS_TOKEN)")
	rootCmd.PersistentFlags().String("project", "", "Default project ID (env: NAVARIS_PROJECT)")
	rootCmd.PersistentFlags().StringP("output", "o", "text", "Output format: json or text")
	rootCmd.PersistentFlags().BoolP("quiet", "q", false, "Quiet output: on operation commands print only the resulting ID; with --output json, suppress non-data fields")

	rootCmd.AddCommand(projectCmd)
	rootCmd.AddCommand(sandboxCmd)
	rootCmd.AddCommand(snapshotCmd)
	rootCmd.AddCommand(imageCmd)
	rootCmd.AddCommand(sessionCmd)
	rootCmd.AddCommand(operationCmd)
	rootCmd.AddCommand(portCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
