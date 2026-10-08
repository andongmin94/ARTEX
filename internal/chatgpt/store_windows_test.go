//go:build windows

package chatgpt

import (
	"bytes"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsDPAPIActualProtectionAndTampering(t *testing.T) {
	plain := []byte("actual-local-DPAPI-credential-fixture")
	encoded, err := protectData(plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, plain) {
		t.Fatal("protected bytes contain plaintext")
	}
	decoded, err := unprotectData(encoded)
	if err != nil || !bytes.Equal(decoded, plain) {
		t.Fatalf("DPAPI roundtrip failed: %v", err)
	}
	encoded[len(encoded)-1] ^= 1
	if _, err = unprotectData(encoded); err == nil {
		t.Fatal("tampered DPAPI ciphertext was accepted")
	}
}

func TestWindowsCredentialACLHasProtectedInheritance(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, path := range []string{c.store.dir, c.store.path} {
		sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		control, _, err := sd.Control()
		if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
			t.Fatalf("credential ACL inherited unrelated access: %v", err)
		}
	}
}
