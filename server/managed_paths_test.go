package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManagedMovesNeverReplaceExistingDestinations(t *testing.T) {
	for _, directory := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "directory"}[directory], func(t *testing.T) {
			data := t.TempDir()
			from := filepath.Join(data, "workspace", "tasks", "1")
			to := filepath.Join(data, "archives", "destination")
			for _, path := range []string{from, to} {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if directory {
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(path, []byte(path), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := managedMove(data, from, to); err == nil {
				t.Fatal("preexisting destination was replaced")
			}
			assertPathExists(t, from)
			assertPathExists(t, to)
			if !directory {
				for _, path := range []string{from, to} {
					if got, err := os.ReadFile(path); err != nil || string(got) != path {
						t.Fatalf("changed file: %q %v", got, err)
					}
				}
			}
		})
	}
}

func TestManagedMovesAndCleanupRejectOutsidePathsAndParentLinks(t *testing.T) {
	data := t.TempDir()
	outside := t.TempDir()
	protected := filepath.Join(outside, "protected.txt")
	if err := os.WriteFile(protected, []byte("preserve outside bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(data, "safe.txt")
	if err := os.WriteFile(inside, []byte("preserve inside bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := managedMove(data, protected, inside); err == nil {
		t.Fatal("outside source accepted")
	}
	if err := managedMove(data, inside, protected); err == nil {
		t.Fatal("outside destination accepted")
	}
	if err := managedRemoveAll(data, protected); err == nil {
		t.Fatal("outside cleanup accepted")
	}
	link := filepath.Join(data, "linked")
	workspaceOutsideLink(t, link, outside)
	if err := managedMove(data, filepath.Join(link, "protected.txt"), filepath.Join(data, "staging", "1")); err == nil {
		t.Fatal("source parent link accepted")
	}
	if err := managedMove(data, inside, filepath.Join(link, "new.txt")); err == nil {
		t.Fatal("destination parent link accepted")
	}
	if err := managedRemoveAll(data, filepath.Join(link, "protected.txt")); err == nil {
		t.Fatal("cleanup parent link accepted")
	}
	if err := managedMkdirAll(data, filepath.Join(link, "new-directory")); err == nil {
		t.Fatal("directory creation parent link accepted")
	}
	if got, err := os.ReadFile(protected); err != nil || string(got) != "preserve outside bytes" {
		t.Fatalf("outside file changed: %q %v", got, err)
	}
	assertPathExists(t, inside)
	assertPathMissing(t, filepath.Join(outside, "new.txt"))
	assertPathMissing(t, filepath.Join(outside, "new-directory"))
}

func TestArchiveJournalDoesNotTruncateExistingFiles(t *testing.T) {
	data := t.TempDir()
	protected := filepath.Join(data, "protected.txt")
	journal := filepath.Join(data, "journal.json")
	if err := os.WriteFile(protected, []byte("keep original application bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(protected, journal); err != nil {
		t.Fatal(err)
	}
	if err := writeArchiveJournal(data, journal, archiveStageJournal{ArchiveID: 1}); err == nil {
		t.Fatal("preexisting journal replaced")
	}
	if got, err := os.ReadFile(protected); err != nil || string(got) != "keep original application bytes" {
		t.Fatalf("journal truncated linked application file: %q %v", got, err)
	}
	entries, err := os.ReadDir(data)
	if err != nil || len(entries) != 2 {
		t.Fatalf("failed journal left temporary files: entries=%d err=%v", len(entries), err)
	}
}
