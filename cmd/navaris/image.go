package main

import (
	"github.com/navaris/navaris/pkg/client"
	"github.com/spf13/cobra"
)

var imageCmd = &cobra.Command{
	Use:   "image",
	Short: "Manage base images",
}

func init() {
	imageCmd.AddCommand(imageListCmd)
	imageCmd.AddCommand(imageGetCmd)
	imageCmd.AddCommand(imagePromoteCmd)
	imageCmd.AddCommand(imageRegisterCmd)
	imageCmd.AddCommand(imageDeleteCmd)
}

var imageListCmd = &cobra.Command{
	Use:   "list",
	Short: "List images",
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")

		c := newClient(cmd)
		images, err := c.ListImages(cmd.Context(), name, "")
		if err != nil {
			return err
		}
		printResult(images, []string{"IMAGE_ID", "NAME", "VERSION", "STATE", "SOURCE", "CREATED_AT"}, func() [][]string {
			rows := make([][]string, len(images))
			for i, img := range images {
				rows[i] = []string{
					img.ImageID, img.Name, img.Version, img.State,
					img.SourceType, img.CreatedAt.Format("2006-01-02T15:04:05Z"),
				}
			}
			return rows
		})
		return nil
	},
}

var imageGetCmd = &cobra.Command{
	Use:   "get <image-id>",
	Short: "Get an image by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient(cmd)
		img, err := c.GetImage(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		printResult(img, []string{"IMAGE_ID", "NAME", "VERSION", "STATE", "SOURCE", "BACKEND", "CREATED_AT"}, func() [][]string {
			return [][]string{{
				img.ImageID, img.Name, img.Version, img.State,
				img.SourceType, img.Backend, img.CreatedAt.Format("2006-01-02T15:04:05Z"),
			}}
		})
		return nil
	},
}

var imagePromoteCmd = &cobra.Command{
	Use:   "promote",
	Short: "Promote a snapshot to a base image",
	RunE: func(cmd *cobra.Command, args []string) error {
		snapshot, _ := cmd.Flags().GetString("snapshot")
		name, _ := cmd.Flags().GetString("name")
		version, _ := cmd.Flags().GetString("version")

		c := newClient(cmd)
		op, err := c.PromoteImage(cmd.Context(), client.CreateImageRequest{
			SnapshotID: snapshot,
			Name:       name,
			Version:    version,
		})
		if err != nil {
			return err
		}
		return handleOperation(cmd.Context(), c, cmd, op, func() (any, error) {
			return c.GetImage(cmd.Context(), op.ResourceID)
		})
	},
}

var imageRegisterCmd = &cobra.Command{
	Use:   "register",
	Short: "Register an externally managed image",
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		version, _ := cmd.Flags().GetString("version")
		backend, _ := cmd.Flags().GetString("backend")
		backendRef, _ := cmd.Flags().GetString("backend-ref")

		c := newClient(cmd)
		img, err := c.RegisterImage(cmd.Context(), client.RegisterImageRequest{
			Name:       name,
			Version:    version,
			Backend:    backend,
			BackendRef: backendRef,
		})
		if err != nil {
			return err
		}
		printResult(img, []string{"IMAGE_ID", "NAME", "VERSION", "STATE", "BACKEND"}, func() [][]string {
			return [][]string{{img.ImageID, img.Name, img.Version, img.State, img.Backend}}
		})
		return nil
	},
}

var imageDeleteCmd = &cobra.Command{
	Use:   "delete <image-id>",
	Short: "Delete an image",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient(cmd)
		op, err := c.DeleteImage(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		return handleOperation(cmd.Context(), c, cmd, op, nil)
	},
}

func init() {
	imageListCmd.Flags().String("name", "", "Filter images by name")

	imagePromoteCmd.Flags().String("snapshot", "", "Snapshot ID to promote (required)")
	_ = imagePromoteCmd.MarkFlagRequired("snapshot")
	imagePromoteCmd.Flags().String("name", "", "Image name")
	imagePromoteCmd.Flags().String("version", "", "Image version")
	addWaitFlags(imagePromoteCmd)

	imageRegisterCmd.Flags().String("name", "", "Image name")
	imageRegisterCmd.Flags().String("version", "", "Image version")
	imageRegisterCmd.Flags().String("backend", "", "Backend type")
	imageRegisterCmd.Flags().String("backend-ref", "", "Backend reference")

	addWaitFlags(imageDeleteCmd)
}
