package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestResendRequiresAction(t *testing.T) {
	raw := json.RawMessage(`{}`)
	if _, err := (resendTool{}).Execute(context.Background(), raw, ExecEnv{}); err == nil {
		t.Fatal("expected error when action is missing")
	}
}

func TestResendUnknownAction(t *testing.T) {
	raw := json.RawMessage(`{"action":"frobnicate"}`)
	if _, err := (resendTool{}).Execute(context.Background(), raw, ExecEnv{}); err == nil {
		t.Fatal("expected error for unknown action")
	}
}

// The inbox handlers below are tested directly (not via Execute) so that
// input validation is exercised without a configured API token or network
// access — validation happens before any HTTP request is issued.

func TestResendInboxAfterBeforeMutuallyExclusive(t *testing.T) {
	_, err := resendInbox(context.Background(), "t", resendArgs{After: "a", Before: "b"})
	if err == nil {
		t.Fatal("expected error when after and before are both set")
	}
}

func TestResendInboxGetRequiresID(t *testing.T) {
	if _, err := resendInboxGet(context.Background(), "t", ""); err == nil {
		t.Fatal("expected error when id is empty")
	}
}

func TestResendInboxAttachmentsRequiresID(t *testing.T) {
	if _, err := resendInboxAttachments(context.Background(), "t", resendArgs{}); err == nil {
		t.Fatal("expected error when id is empty")
	}
	if _, err := resendInboxAttachments(context.Background(), "t", resendArgs{ID: "e", After: "a", Before: "b"}); err == nil {
		t.Fatal("expected error when after and before are both set")
	}
}

func TestResendInboxAttachmentRequiresIDs(t *testing.T) {
	if _, err := resendInboxAttachment(context.Background(), "t", resendArgs{}); err == nil {
		t.Fatal("expected error when id is empty")
	}
	if _, err := resendInboxAttachment(context.Background(), "t", resendArgs{ID: "e"}); err == nil {
		t.Fatal("expected error when attachment_id is empty")
	}
}

func TestInferContentType(t *testing.T) {
	cases := map[string]string{
		".html": "text/html",
		".HTML": "text/html",
		".htm":  "text/html",
		".txt":  "text/plain",
		".md":   "text/plain",
		".pdf":  "application/pdf",
		".png":  "image/png",
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".gif":  "image/gif",
		".svg":  "image/svg+xml",
		".csv":  "text/csv",
		".json": "application/json",
		".zip":  "application/zip",
		".xyz":  "application/octet-stream",
	}
	for in, want := range cases {
		if got := inferContentType(in); got != want {
			t.Fatalf("inferContentType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResendBuildAttachments(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(p, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	atts, err := resendBuildAttachments([]string{p})
	if err != nil {
		t.Fatal(err)
	}
	if len(atts) != 1 {
		t.Fatalf("got %d attachments, want 1", len(atts))
	}
	a := atts[0]
	if a["filename"] != "note.txt" {
		t.Fatalf("filename = %q, want note.txt", a["filename"])
	}
	if a["content"] != base64.StdEncoding.EncodeToString([]byte("hello")) {
		t.Fatalf("content = %q", a["content"])
	}
	if a["content_type"] != "text/plain" {
		t.Fatalf("content_type = %q, want text/plain", a["content_type"])
	}
}

func TestResendBuildAttachmentsMissing(t *testing.T) {
	if _, err := resendBuildAttachments([]string{"/no/such/file.txt"}); err == nil {
		t.Fatal("expected error for missing file")
	}
}
