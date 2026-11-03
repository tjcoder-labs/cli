package ledger

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecordAndHas(t *testing.T) {
	l, err := Load(filepath.Join(t.TempDir(), "ledger.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := l.Has("email", "msg-1"); ok {
		t.Fatal("expected absent before record")
	}
	isNew, err := l.Record(Entry{Namespace: "email", Key: "msg-1", Kind: "sent"})
	if err != nil {
		t.Fatal(err)
	}
	if !isNew {
		t.Fatal("expected new entry")
	}
	e, ok := l.Has("Email", "MSG-1") // IDs are case-insensitive
	if !ok || e.Kind != "sent" {
		t.Fatalf("expected recorded entry, got %+v ok=%v", e, ok)
	}
}

func TestRecordReplaceDoesNotDuplicateRecent(t *testing.T) {
	l, _ := Load(filepath.Join(t.TempDir(), "ledger.json"))
	l.Record(Entry{Namespace: "social", Key: "k", Kind: "like"})
	l.Record(Entry{Namespace: "social", Key: "k", Kind: "like"})
	if n := l.Count("social", "like"); n != 1 {
		t.Fatalf("expected 1 entry after replace, got %d", n)
	}
	if len(l.Recent) != 1 {
		t.Fatalf("expected Recent len 1, got %d", len(l.Recent))
	}
}

func TestTTLExpiry(t *testing.T) {
	l, _ := Load(filepath.Join(t.TempDir(), "ledger.json"))
	l.Record(Entry{Namespace: "n", Key: "k", TTL: 50 * time.Millisecond})
	if _, ok := l.Has("n", "k"); !ok {
		t.Fatal("expected present before expiry")
	}
	time.Sleep(60 * time.Millisecond)
	if _, ok := l.Has("n", "k"); ok {
		t.Fatal("expected expired")
	}
}

func TestDeleteAndClear(t *testing.T) {
	l, _ := Load(filepath.Join(t.TempDir(), "ledger.json"))
	l.Record(Entry{Namespace: "a", Key: "1"})
	l.Record(Entry{Namespace: "a", Key: "2"})
	l.Record(Entry{Namespace: "b", Key: "1"})
	if !l.Delete("a", "1") {
		t.Fatal("delete should report present")
	}
	if _, ok := l.Has("a", "1"); ok {
		t.Fatal("expected deleted")
	}
	if n := l.Clear("a"); n != 1 {
		t.Fatalf("expected clear to remove 1 (a:2), got %d", n)
	}
	if n := l.Count("", ""); n != 1 {
		t.Fatalf("expected 1 remaining (b:1), got %d", n)
	}
}

func TestPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	l, _ := Load(path)
	l.Record(Entry{Namespace: "email", Key: "m", Note: "hello", Data: []byte(`{"x":1}`)})

	l2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := l2.Has("email", "m")
	if !ok || e.Note != "hello" || len(e.Data) == 0 {
		t.Fatalf("persistence round-trip failed: %+v", e)
	}
}

func TestRecordRequiresNamespaceAndKey(t *testing.T) {
	l, _ := Load(filepath.Join(t.TempDir(), "ledger.json"))
	if _, err := l.Record(Entry{Namespace: "x"}); err == nil {
		t.Fatal("expected error for missing key")
	}
	if _, err := l.Record(Entry{Key: "x"}); err == nil {
		t.Fatal("expected error for missing namespace")
	}
}

func TestSaveFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	l, _ := Load(path)
	if _, err := l.Record(Entry{Namespace: "n", Key: "k"}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600, got %o", fi.Mode().Perm())
	}
}
