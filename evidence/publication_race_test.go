package evidence

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type evidencePublicationRaceReader struct {
	reader *bytes.Reader
	once   func() error
}

func (r *evidencePublicationRaceReader) Read(p []byte) (int, error) {
	if r.once != nil {
		fn := r.once
		r.once = nil
		if err := fn(); err != nil {
			return 0, err
		}
	}
	return r.reader.Read(p)
}

func TestEvidencePublicationRejectsReplacedStagingDirectory(t *testing.T) {
	root := evidenceBodyFixtureRoot(t)
	body := []byte("verified local publication fixture")
	hash := evidenceBodyHash(body)
	name, err := hashPath(".", hash)
	if err != nil {
		t.Fatal(err)
	}
	protectedBody := bytes.Repeat([]byte("x"), len(body))
	protectedPath := filepath.Join(t.TempDir(), "protected.bin")
	if err := os.WriteFile(protectedPath, protectedBody, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Unix(946684800, 0)
	if err := os.Chtimes(protectedPath, old, old); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(protectedPath)
	if err != nil {
		t.Fatal(err)
	}
	var renameErr error
	var replaced bool
	source := &evidencePublicationRaceReader{reader: bytes.NewReader(body)}
	source.once = func() error {
		stage, err := root.Open(".staging")
		if err != nil {
			return err
		}
		entries, readErr := stage.Readdirnames(-1)
		if err := errors.Join(readErr, stage.Close()); err != nil {
			return err
		}
		if len(entries) != 1 {
			return errors.New("expected exactly one unpublished evidence temporary file")
		}
		// The producer still holds its original temporary file descriptor. Move
		// that directory and substitute the same name while it writes the body.
		renameErr = root.Rename(".staging", ".original-staging")
		if renameErr != nil {
			return nil
		}
		if err := root.Mkdir(".staging", 0o700); err != nil {
			return err
		}
		if err := os.Link(protectedPath, filepath.Join(root.Name(), ".staging", entries[0])); err != nil {
			return err
		}
		replaced = true
		return nil
	}
	_, publishErr := publishBody(root, source, int64(len(body)), hash)
	after, err := os.Stat(protectedPath)
	if err != nil || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("protected hardlink timestamp changed: %v %v", after, err)
	}
	if got, err := os.ReadFile(protectedPath); err != nil || !bytes.Equal(got, protectedBody) {
		t.Fatalf("protected hardlink bytes changed: %q %v", got, err)
	}
	if renameErr != nil {
		t.Skipf("OS refused replacement of the staging directory with an open producer file: %v", renameErr)
	}
	if !replaced {
		t.Fatalf("staging replacement fixture did not run: %v", publishErr)
	}
	file, err := root.Open(name)
	if err != nil {
		t.Fatalf("substituted final body was unexpectedly removed: %v (publication error: %v)", err, publishErr)
	}
	info, statErr := file.Stat()
	got, readErr := io.ReadAll(file)
	if err := errors.Join(statErr, readErr, file.Close()); err != nil {
		t.Fatal(err)
	}
	if publishErr != nil {
		// Post-publication verification must refuse these bytes without deleting
		// an entry that may now belong to a concurrently introduced file.
		if !bytes.Equal(got, protectedBody) || !os.SameFile(info, after) {
			t.Fatalf("refused publication changed the substituted protected entry: %q", got)
		}
		return
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("publication returned success for a substituted unverified body: got %q, want %q", got, body)
	}
}
