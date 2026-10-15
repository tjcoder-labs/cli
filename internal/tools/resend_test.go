package tools

import (
	"context"
	"encoding/json"
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

func TestInboxLimit(t *testing.T) {
	cases := map[int]int{
		-5:  20,
		0:   20,
		1:   1,
		20:  20,
		50:  50,
		100: 100,
		101: 100,
		999: 100,
	}
	for in, want := range cases {
		if got := inboxLimit(in); got != want {
			t.Fatalf("inboxLimit(%d) = %d, want %d", in, got, want)
		}
	}
}
