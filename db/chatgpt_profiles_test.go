package db

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func TestSubscriptionSchemaUpgradePreservesV1Data(t *testing.T) {
	path := testDSN(t)
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	v1 := strings.Replace(schemaSQL, "\t auth_method      TEXT NOT NULL DEFAULT 'api-key' CHECK (auth_method IN ('api-key','chatgpt')),", "", 1)
	if strings.Contains(v1, "auth_method") {
		t.Fatal("fixture failed to restore v1 schema")
	}
	for _, stmt := range []string{v1, seedSQL, fmt.Sprintf("PRAGMA application_id=%d", businessApplicationID), "PRAGMA user_version=1", `INSERT INTO llm_profiles(name,format,model,api_key,is_default) VALUES('retained','openai','fixture','retained-key',1)`, `INSERT INTO settings(key,value) VALUES('custom-retained','value')`} {
		if _, err := raw.Exec(stmt); err != nil {
			raw.Close()
			t.Fatal(err)
		}
	}
	raw.Close()
	d := openBusinessFixture(t, path)
	p, err := d.ActiveProfile()
	if err != nil || p == nil || p.AuthMethod != "api-key" || p.APIKey != "retained-key" || p.Name != "retained" {
		t.Fatalf("profile lost: %+v %v", p, err)
	}
	if value, ok, err := d.GetSetting("custom-retained"); err != nil || !ok || value != "value" {
		t.Fatalf("user setting lost: %q %v %v", value, ok, err)
	}
	var version int
	if err := d.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 2 {
		t.Fatalf("version=%d %v", version, err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d = openBusinessFixture(t, path)
	if p, err := d.ActiveProfile(); err != nil || p == nil || p.APIKey != "retained-key" {
		t.Fatalf("reopen=%+v %v", p, err)
	}
}

func TestSubscriptionProfilesNeverStoreCredentials(t *testing.T) {
	d := openBusinessFixture(t, testDSN(t))
	p := &LLMProfile{Name: "subscription", Format: "openai-responses", AuthMethod: "chatgpt", Model: "fixture-model", Streaming: true, PoolExclude: true}
	id, err := d.SaveProfile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.SetActiveProfile(id); err != nil {
		t.Fatal(err)
	}
	loaded, err := d.ActiveProfile()
	if err != nil || loaded.AuthMethod != "chatgpt" || loaded.APIKey != "" || loaded.BaseURL != "" {
		t.Fatalf("stored credentials: %+v %v", loaded, err)
	}
	profiles, err := d.PoolProfiles()
	if err != nil || len(profiles) != 1 || profiles[0].ID != id {
		t.Fatalf("active subscription absent: %+v %v", profiles, err)
	}
	p.ID, p.APIKey = id, "must-not-store"
	if _, err := d.SaveProfile(p); err == nil {
		t.Fatal("subscription accepted API secret")
	}
	p.APIKey, p.BaseURL = "", "https://example.invalid"
	if _, err := d.SaveProfile(p); err == nil {
		t.Fatal("subscription accepted alternate endpoint")
	}
}
