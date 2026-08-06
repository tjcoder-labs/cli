package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// inputHistoryFileName is the file inside prefsDirName that holds
// the serialized input history (most-recent-last ordering).
const inputHistoryFileName = "input_history.json"

// inputHistoryMax is the maximum number of entries retained. Older
// entries are trimmed from the front when the list grows past this.
const inputHistoryMax = 500

// InputHistory is the on-disk shape of the global input history.
type InputHistory struct {
	Entries []string `json:"entries"`
}

// inputHistoryMu serializes concurrent reads/writes of the history
// file from the TUI's event loop and any background goroutine.
var inputHistoryMu sync.Mutex

// InputHistoryPath returns the absolute path of the input history
// file. It honors $XDG_CONFIG_HOME and falls back to ~/.config,
// mirroring PrefsPath so both files share the same directory.
func InputHistoryPath() (string, error) {
	root := os.Getenv("XDG_CONFIG_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(home, ".config")
	}
	return filepath.Join(root, prefsDirName, inputHistoryFileName), nil
}

// LoadInputHistory reads the persisted input history. A missing
// file is not an error: it returns an empty slice.
func LoadInputHistory() ([]string, error) {
	inputHistoryMu.Lock()
	defer inputHistoryMu.Unlock()

	path, err := InputHistoryPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var h InputHistory
	if err := json.Unmarshal(data, &h); err != nil {
		return nil, nil
	}
	return h.Entries, nil
}

// AppendInputHistory appends entry to the persisted history, trimming
// the front when the list exceeds inputHistoryMax. Empty or
// whitespace-only entries are silently dropped. If the entry matches
// the last recorded entry it is not duplicated.
func AppendInputHistory(entry string) error {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return nil
	}

	inputHistoryMu.Lock()
	defer inputHistoryMu.Unlock()

	path, err := InputHistoryPath()
	if err != nil {
		return err
	}

	// Load existing
	entries := []string{}
	data, err := os.ReadFile(path)
	if err == nil {
		var h InputHistory
		if json.Unmarshal(data, &h) == nil {
			entries = h.Entries
		}
	}

	// Deduplicate against the last entry.
	if len(entries) > 0 && entries[len(entries)-1] == entry {
		return nil
	}

	entries = append(entries, entry)

	// Trim from the front to keep the list bounded.
	if len(entries) > inputHistoryMax {
		entries = entries[len(entries)-inputHistoryMax:]
	}

	// Write back
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	h := InputHistory{Entries: entries}
	data, err = json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".input_history-*.json.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}