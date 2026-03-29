//go:build integration

package integration

import (
	"context"
	"os"
	"testing"

	"github.com/navaris/navaris/pkg/client"
)

func TestImageRegisterListGetDelete(t *testing.T) {
	c := newClient()
	ctx := context.Background()

	img, err := c.RegisterImage(ctx, client.RegisterImageRequest{
		Name:       "test-image",
		Version:    "1.0",
		Backend:    "incus",
		BackendRef: baseImage(),
	})
	if err != nil {
		t.Fatalf("register image: %v", err)
	}
	t.Logf("registered image %s", img.ImageID)

	t.Cleanup(func() {
		op, err := c.DeleteImage(context.Background(), img.ImageID)
		if err != nil {
			t.Logf("warning: delete image: %v", err)
			return
		}
		c.WaitForOperation(context.Background(), op.OperationID, waitOpts())
	})

	got, err := c.GetImage(ctx, img.ImageID)
	if err != nil {
		t.Fatalf("get image: %v", err)
	}
	if got.Name != "test-image" {
		t.Fatalf("expected name test-image, got %s", got.Name)
	}

	images, err := c.ListImages(ctx, "test-image", "")
	if err != nil {
		t.Fatalf("list images: %v", err)
	}
	found := false
	for _, i := range images {
		if i.ImageID == img.ImageID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("registered image not found in list")
	}
}

func TestImagePromoteFromSnapshot(t *testing.T) {
	if os.Getenv("NAVARIS_SKIP_SNAPSHOTS") == "1" {
		t.Skip("snapshots not supported by this backend")
	}
	c := newClient()
	ctx := context.Background()

	proj := createTestProject(t, c)
	sandboxID := createTestSandbox(t, c, proj.ProjectID, "img-promote-sbx")

	stopOp, err := c.StopSandboxAndWait(ctx, sandboxID, client.StopSandboxRequest{}, waitOpts())
	if err != nil {
		t.Fatalf("stop sandbox: %v", err)
	}
	if stopOp.State != client.OpSucceeded {
		t.Fatalf("stop failed: %s %s", stopOp.State, stopOp.ErrorText)
	}

	snapOp, err := c.CreateSnapshot(ctx, sandboxID, client.CreateSnapshotRequest{
		Label:           "for-image-promote",
		ConsistencyMode: "stopped",
	})
	if err != nil {
		t.Fatalf("create snapshot: %v", err)
	}
	snapOp, err = c.WaitForOperation(ctx, snapOp.OperationID, waitOpts())
	if err != nil {
		t.Fatalf("wait snapshot: %v", err)
	}
	if snapOp.State != client.OpSucceeded {
		t.Fatalf("snapshot failed: %s %s", snapOp.State, snapOp.ErrorText)
	}
	snapshotID := snapOp.ResourceID

	t.Cleanup(func() {
		delOp, err := c.DeleteSnapshot(context.Background(), snapshotID)
		if err != nil {
			return
		}
		c.WaitForOperation(context.Background(), delOp.OperationID, waitOpts())
	})

	promoteOp, err := c.PromoteImage(ctx, client.CreateImageRequest{
		SnapshotID: snapshotID,
		Name:       "promoted-test",
		Version:    "1.0",
	})
	if err != nil {
		t.Fatalf("promote image: %v", err)
	}
	promoteOp, err = c.WaitForOperation(ctx, promoteOp.OperationID, waitOpts())
	if err != nil {
		t.Fatalf("wait promote: %v", err)
	}
	if promoteOp.State != client.OpSucceeded {
		t.Fatalf("promote failed: %s %s", promoteOp.State, promoteOp.ErrorText)
	}
	imageID := promoteOp.ResourceID
	t.Logf("promoted image %s", imageID)

	t.Cleanup(func() {
		delOp, err := c.DeleteImage(context.Background(), imageID)
		if err != nil {
			return
		}
		c.WaitForOperation(context.Background(), delOp.OperationID, waitOpts())
	})

	img, err := c.GetImage(ctx, imageID)
	if err != nil {
		t.Fatalf("get promoted image: %v", err)
	}
	if img.Name != "promoted-test" {
		t.Fatalf("expected name promoted-test, got %s", img.Name)
	}
}
