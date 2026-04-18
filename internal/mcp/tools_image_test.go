package mcp_test

import (
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/navaris/navaris/pkg/client"
)

func TestImageList_AcceptsNoFilters(t *testing.T) {
	sess, _ := startMCPTestServer(t, false)

	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "image_list",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}
	got := decodeJSONResult(t, res)
	arr, ok := got.([]any)
	if !ok {
		t.Fatalf("expected array result, got %T", got)
	}
	if len(arr) != 0 {
		t.Errorf("expected 0 images, got %d", len(arr))
	}
}

func TestImageList_ReturnsRegisteredImages(t *testing.T) {
	sess, apiURL := startMCPTestServer(t, false)
	c := client.NewClient(client.WithURL(apiURL), client.WithToken("test-token"))

	img, err := c.RegisterImage(t.Context(), client.RegisterImageRequest{
		Name:         "test-image",
		Version:      "1.0.0",
		Backend:      "incus",
		BackendRef:   "test-ref",
		Architecture: "amd64",
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "image_list",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}
	got := decodeJSONResult(t, res)
	arr, ok := got.([]any)
	if !ok {
		t.Fatalf("expected array result, got %T", got)
	}
	if len(arr) != 1 {
		t.Fatalf("expected 1 image, got %d", len(arr))
	}
	first, ok := arr[0].(map[string]any)
	if !ok {
		t.Fatalf("expected image object, got %T", arr[0])
	}
	if id, _ := first["ImageID"].(string); id != img.ImageID {
		t.Errorf("expected ImageID=%q, got %q", img.ImageID, id)
	}
}

func TestImageList_FilterByName(t *testing.T) {
	sess, apiURL := startMCPTestServer(t, false)
	c := client.NewClient(client.WithURL(apiURL), client.WithToken("test-token"))

	first, err := c.RegisterImage(t.Context(), client.RegisterImageRequest{
		Name:         "first",
		Version:      "1.0.0",
		Backend:      "incus",
		BackendRef:   "ref-first",
		Architecture: "amd64",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.RegisterImage(t.Context(), client.RegisterImageRequest{
		Name:         "second",
		Version:      "1.0.0",
		Backend:      "incus",
		BackendRef:   "ref-second",
		Architecture: "amd64",
	}); err != nil {
		t.Fatal(err)
	}

	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "image_list",
		Arguments: map[string]any{"name": "first"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}
	got := decodeJSONResult(t, res)
	arr, ok := got.([]any)
	if !ok {
		t.Fatalf("expected array result, got %T", got)
	}
	if len(arr) != 1 {
		t.Fatalf("expected 1 image, got %d", len(arr))
	}
	obj, ok := arr[0].(map[string]any)
	if !ok {
		t.Fatalf("expected image object, got %T", arr[0])
	}
	if id, _ := obj["ImageID"].(string); id != first.ImageID {
		t.Errorf("expected ImageID=%q, got %q", first.ImageID, id)
	}
}

func TestImageGet_Found(t *testing.T) {
	sess, apiURL := startMCPTestServer(t, false)
	c := client.NewClient(client.WithURL(apiURL), client.WithToken("test-token"))

	img, err := c.RegisterImage(t.Context(), client.RegisterImageRequest{
		Name:         "get-image",
		Version:      "1.0.0",
		Backend:      "incus",
		BackendRef:   "get-ref",
		Architecture: "amd64",
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "image_get",
		Arguments: map[string]any{"image_id": img.ImageID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}
	got := decodeJSONResult(t, res)
	obj, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected object, got %T", got)
	}
	if id, _ := obj["ImageID"].(string); id != img.ImageID {
		t.Errorf("expected ImageID=%q, got %q", img.ImageID, id)
	}
}

func TestImageGet_NotFound(t *testing.T) {
	sess, _ := startMCPTestServer(t, false)
	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "image_get",
		Arguments: map[string]any{"image_id": "nonexistent"},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError=true for missing image, got %v", res.Content)
	}
	text := ""
	for _, c := range res.Content {
		if tc, ok := c.(*mcpsdk.TextContent); ok {
			text += tc.Text
		}
	}
	if !strings.Contains(strings.ToLower(text), "not found") {
		t.Errorf("expected error text to mention 'not found', got %q", text)
	}
}
