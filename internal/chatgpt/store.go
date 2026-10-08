package chatgpt

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

const maxCredentialBytes = 2 << 20

type credentialRecord struct {
	Version           int             `json:"version"`
	HostID            string          `json:"ext_agent_host_id"`
	ClientID          string          `json:"client_id,omitempty"`
	Issuer            string          `json:"issuer,omitempty"`
	Subject           string          `json:"subject,omitempty"`
	Email             string          `json:"email,omitempty"`
	Name              string          `json:"name,omitempty"`
	Nonce             string          `json:"nonce,omitempty"`
	IDToken           string          `json:"id_token,omitempty"`
	AccessToken       string          `json:"access_token,omitempty"`
	RefreshToken      string          `json:"refresh_token,omitempty"`
	TokenType         string          `json:"token_type,omitempty"`
	ExpiresAt         time.Time       `json:"expires_at,omitempty"`
	Scopes            []string        `json:"scopes,omitempty"`
	EarliestRefreshAt json.RawMessage `json:"earliest_refresh_at,omitempty"`
}

func (r *credentialRecord) clearTokens() {
	r.IDToken = ""
	r.AccessToken = ""
	r.RefreshToken = ""
	r.TokenType = ""
	r.Nonce = ""
	r.ExpiresAt = time.Time{}
	r.Scopes = nil
	r.EarliestRefreshAt = nil
}

type credentialStore struct{ dir, path string }

func newCredentialStore(home string) (*credentialStore, error) {
	if !filepath.IsAbs(home) {
		return nil, errStorage
	}
	info, err := os.Stat(home)
	if err != nil || !info.IsDir() {
		return nil, errStorage
	}
	dir := filepath.Join(home, "chatgpt")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err = checkRegularPath(dir, true); err != nil {
		return nil, err
	}
	if err = protectPermissions(dir, true); err != nil {
		return nil, err
	}
	return &credentialStore{dir: dir, path: filepath.Join(dir, "credentials")}, nil
}

func checkRegularPath(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || info.IsDir() != directory || (!directory && !info.Mode().IsRegular()) {
		return errStorage
	}
	return checkPlatformPath(path)
}

func (s *credentialStore) loadOrCreate() (*credentialRecord, error) {
	r, err := s.load()
	if errors.Is(err, os.ErrNotExist) {
		hostID, randomErr := uuid.NewRandom()
		if randomErr != nil {
			return nil, randomErr
		}
		r = &credentialRecord{Version: 1, HostID: "urn:uuid:" + hostID.String()}
		err = s.save(r)
	}
	return r, err
}

func (s *credentialStore) load() (*credentialRecord, error) {
	if err := checkRegularPath(s.path, false); err != nil {
		return nil, err
	}
	if err := protectPermissions(s.path, false); err != nil {
		return nil, err
	}
	f, err := os.Open(s.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	encoded, err := io.ReadAll(io.LimitReader(f, maxCredentialBytes+1))
	if err != nil || len(encoded) > maxCredentialBytes {
		return nil, errStorage
	}
	plain, err := unprotectData(encoded)
	if err != nil {
		return nil, errStorage
	}
	defer clear(plain)
	var r credentialRecord
	if err = decodeBoundedJSON(strings.NewReader(string(plain)), &r); err != nil {
		return nil, errStorage
	}
	if r.Version != 1 || !strings.HasPrefix(r.HostID, "urn:uuid:") {
		return nil, errStorage
	}
	hostID, err := uuid.Parse(strings.TrimPrefix(r.HostID, "urn:uuid:"))
	if err != nil || hostID.Version() != 4 || hostID.Variant() != uuid.RFC4122 {
		return nil, errStorage
	}
	if r.ClientID == dynamicClientID || r.Subject != "" && (r.ClientID == "" || r.Issuer == "") {
		return nil, errStorage
	}
	if r.Issuer != "" && r.Issuer != issuer || r.IDToken != "" && (r.Subject == "" || r.Nonce == "") || r.RefreshToken != "" && r.AccessToken == "" {
		return nil, errStorage
	}
	if r.AccessToken != "" && (r.Subject == "" || !strings.EqualFold(r.TokenType, "Bearer") || r.ExpiresAt.IsZero()) {
		return nil, errStorage
	}
	return &r, nil
}

func (s *credentialStore) save(r *credentialRecord) error {
	if err := checkRegularPath(s.dir, true); err != nil {
		return err
	}
	if _, err := os.Lstat(s.path); err == nil {
		if err = checkRegularPath(s.path, false); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	plain, err := json.Marshal(r)
	if err != nil {
		return err
	}
	defer clear(plain)
	encoded, err := protectData(plain)
	if err != nil {
		return err
	}
	defer clear(encoded)
	f, err := os.CreateTemp(s.dir, ".credentials-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = protectPermissions(tmp, false); err == nil {
		_, err = f.Write(encoded)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return replaceCredentialFile(tmp, s.path)
}

func (s *credentialStore) lock(ctx context.Context) (func(), error) {
	path := filepath.Join(s.dir, "lock")
	if err := checkRegularPath(s.dir, true); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(path); err == nil {
		if err = checkRegularPath(path, false); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	if err = protectPermissions(path, false); err != nil {
		f.Close()
		return nil, err
	}
	if err = lockFile(ctx, f); err != nil {
		f.Close()
		return nil, err
	}
	return func() { unlockFile(f); f.Close() }, nil
}

func decodeBoundedJSON(body io.Reader, value any) error {
	data, err := io.ReadAll(io.LimitReader(body, maxCredentialBytes+1))
	if err != nil || len(data) > maxCredentialBytes {
		return errInvalidResponse
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	if err = dec.Decode(value); err != nil {
		return errInvalidResponse
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return errInvalidResponse
	}
	return nil
}
