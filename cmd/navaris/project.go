package main

import (
	"fmt"

	"github.com/navaris/navaris/pkg/client"
	"github.com/spf13/cobra"
)

var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "Manage projects",
}

func init() {
	projectCmd.AddCommand(projectCreateCmd)
	projectCmd.AddCommand(projectListCmd)
	projectCmd.AddCommand(projectGetCmd)
	projectCmd.AddCommand(projectUpdateCmd)
	projectCmd.AddCommand(projectDeleteCmd)
}

var projectCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new project",
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		c := newClient(cmd)
		p, err := c.CreateProject(cmd.Context(), client.CreateProjectRequest{Name: name})
		if err != nil {
			return err
		}
		printResult(p, []string{"PROJECT_ID", "NAME", "CREATED_AT"}, func() [][]string {
			return [][]string{{p.ProjectID, p.Name, p.CreatedAt.Format("2006-01-02T15:04:05Z")}}
		})
		return nil
	},
}

var projectListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all projects",
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient(cmd)
		projects, err := c.ListProjects(cmd.Context())
		if err != nil {
			return err
		}
		printResult(projects, []string{"PROJECT_ID", "NAME", "CREATED_AT"}, func() [][]string {
			rows := make([][]string, len(projects))
			for i, p := range projects {
				rows[i] = []string{p.ProjectID, p.Name, p.CreatedAt.Format("2006-01-02T15:04:05Z")}
			}
			return rows
		})
		return nil
	},
}

var projectGetCmd = &cobra.Command{
	Use:   "get <project-id>",
	Short: "Get a project by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient(cmd)
		p, err := c.GetProject(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		printResult(p, []string{"PROJECT_ID", "NAME", "CREATED_AT", "UPDATED_AT"}, func() [][]string {
			return [][]string{{
				p.ProjectID, p.Name,
				p.CreatedAt.Format("2006-01-02T15:04:05Z"),
				p.UpdatedAt.Format("2006-01-02T15:04:05Z"),
			}}
		})
		return nil
	},
}

var projectUpdateCmd = &cobra.Command{
	Use:   "update <project-id>",
	Short: "Update a project",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		c := newClient(cmd)
		p, err := c.UpdateProject(cmd.Context(), args[0], client.UpdateProjectRequest{Name: name})
		if err != nil {
			return err
		}
		printResult(p, []string{"PROJECT_ID", "NAME", "UPDATED_AT"}, func() [][]string {
			return [][]string{{p.ProjectID, p.Name, p.UpdatedAt.Format("2006-01-02T15:04:05Z")}}
		})
		return nil
	},
}

var projectDeleteCmd = &cobra.Command{
	Use:   "delete <project-id>",
	Short: "Delete a project",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient(cmd)
		if err := c.DeleteProject(cmd.Context(), args[0]); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Project %s deleted\n", args[0])
		return nil
	},
}

func init() {
	projectCreateCmd.Flags().String("name", "", "Project name (required)")
	_ = projectCreateCmd.MarkFlagRequired("name")

	projectUpdateCmd.Flags().String("name", "", "New project name")
}
