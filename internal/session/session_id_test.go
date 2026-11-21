package session

import (
	"regexp"
	"testing"
)

func TestSessionIDRoundTrip(t *testing.T) {
	id := NewSessionID()
	if !regexp.MustCompile(`^cs_[0-9a-f]{24}$`).MatchString(id) {
		t.Fatalf("unexpected id %q", id)
	}
	if NewSessionID() == id {
		t.Fatal("ids must be unique")
	}
	root := t.TempDir()
	if err := Save(root, State{SessionID: id, CurrentAgent: "a"}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := Load(root)
	if err != nil || !ok || got.SessionID != id {
		t.Fatalf("session id not persisted: %+v %v %v", got.SessionID, ok, err)
	}
}

func TestTranscriptSyncPref(t *testing.T) {
	withXDGConfigHome(t)
	if !TranscriptSyncEnabled() {
		t.Fatal("default should be on")
	}
	if err := SetTranscriptSync(false); err != nil {
		t.Fatal(err)
	}
	if TranscriptSyncEnabled() {
		t.Fatal("opt-out not persisted")
	}
	if err := SetLastModel(true, "m"); err != nil {
		t.Fatal(err)
	}
	if TranscriptSyncEnabled() {
		t.Fatal("unrelated pref write reset the opt-out")
	}
}
