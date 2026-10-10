package evidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func evidenceBodyFixtureRoot(t *testing.T) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return root
}

func evidenceBodyHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func assertEvidenceStageEmpty(t *testing.T, root *os.Root) {
	t.Helper()
	dir, err := root.Open(".staging")
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	names, err := dir.Readdirnames(-1)
	if err != nil || len(names) != 0 {
		t.Fatalf("publication left temporary files: %v %v", names, err)
	}
}

func TestEvidencePublicationPreservesExistingHardLinks(t *testing.T) {
	body := []byte("complete local evidence body")
	hash := evidenceBodyHash(body)
	for _, valid := range []bool{false, true} {
		t.Run(map[bool]string{false: "corrupt", true: "valid"}[valid], func(t *testing.T) {
			root := evidenceBodyFixtureRoot(t)
			name, err := hashPath(".", hash)
			if err != nil {
				t.Fatal(err)
			}
			if err := root.MkdirAll(filepath.Dir(name), 0o700); err != nil {
				t.Fatal(err)
			}
			prior := bytes.Repeat([]byte("x"), len(body))
			if valid {
				prior = body
			}
			protected := filepath.Join(t.TempDir(), "protected.bin")
			if err := os.WriteFile(protected, prior, 0o600); err != nil {
				t.Fatal(err)
			}
			old := time.Unix(946684800, 0)
			if err := os.Chtimes(protected, old, old); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(protected)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Link(protected, filepath.Join(root.Name(), name)); err != nil {
				t.Fatal(err)
			}
			_, publishErr := publishBody(root, bytes.NewReader(body), int64(len(body)), hash)
			if (publishErr == nil) != valid {
				t.Fatalf("existing body validation err=%v valid=%v", publishErr, valid)
			}
			after, err := os.Stat(protected)
			if err != nil || !after.ModTime().Equal(before.ModTime()) {
				t.Fatalf("preexisting hardlink timestamp changed: %v %v", after, err)
			}
			if got, err := os.ReadFile(protected); err != nil || !bytes.Equal(got, prior) {
				t.Fatalf("preexisting hardlink bytes changed: %q %v", got, err)
			}
			assertEvidenceStageEmpty(t, root)
			if !valid {
				if err := root.Remove(name); err != nil {
					t.Fatal(err)
				}
				if _, err := publishBody(root, bytes.NewReader(body), int64(len(body)), hash); err != nil {
					t.Fatal("publication did not recover after disposing its corrupt fixture", err)
				}
			}
		})
	}
}

type evidenceFailureReader struct{}

func (evidenceFailureReader) Read([]byte) (int, error) {
	return 0, errors.New("controlled source failure")
}

func TestEvidenceFailedCopyLeavesFinalNameAbsentAndRecovers(t *testing.T) {
	root := evidenceBodyFixtureRoot(t)
	body := []byte("complete evidence bytes")
	hash := evidenceBodyHash(body)
	name, _ := hashPath(".", hash)
	failing := io.MultiReader(bytes.NewReader(body[:5]), evidenceFailureReader{})
	if _, err := publishBody(root, failing, int64(len(body)), hash); err == nil {
		t.Fatal("source failure accepted")
	}
	if _, err := root.Stat(name); !os.IsNotExist(err) {
		t.Fatalf("failed copy published partial final body: %v", err)
	}
	assertEvidenceStageEmpty(t, root)
	for _, bad := range [][]byte{body[:3], bytes.Repeat([]byte("x"), len(body)), append(append([]byte(nil), body...), 'x')} {
		if _, err := publishBody(root, bytes.NewReader(bad), int64(len(body)), hash); err == nil {
			t.Fatal("incomplete or changed body accepted")
		}
		assertEvidenceStageEmpty(t, root)
	}
	if _, err := publishBody(root, bytes.NewReader(body), int64(len(body)), hash); err != nil {
		t.Fatal("publication did not recover", err)
	}
	f, err := openVerifiedBody(root, hash, int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if got, err := io.ReadAll(f); err != nil || !bytes.Equal(got, body) {
		t.Fatalf("verified body was not rewound: %q %v", got, err)
	}
}

func TestEvidenceConcurrentPublicationRetainsCompleteBody(t *testing.T) {
	root := evidenceBodyFixtureRoot(t)
	body := bytes.Repeat([]byte("concurrent local body "), 3000)
	hash := evidenceBodyHash(body)
	var group sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := publishBody(root, bytes.NewReader(body), int64(len(body)), hash)
			errs <- err
		}()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	f, err := openVerifiedBody(root, hash, int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	assertEvidenceStageEmpty(t, root)
}
