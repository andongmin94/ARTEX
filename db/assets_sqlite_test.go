package db

import "testing"

func TestSQLiteNetworkScopeBoundariesAndReopen(t *testing.T) {
	filename := testDSN(t)
	d, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	task, err := d.CreateTask("CIDR fixture", "local", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, network := range []string{"2001:db8::/126", "198.51.100.8/31"} {
		if _, err := d.Assets().AddAgentScope(task.ID, "cidr", network, "fixture", "manual"); err != nil {
			t.Fatal(err)
		}
	}
	for _, ip := range []string{"2001:db8::", "2001:db8::3", "2001:db8::4", "::ffff:198.51.100.9", "198.51.100.10"} {
		if _, err := d.Assets().UpsertIP(UpsertIPReq{IP: ip, TaskID: task.ID}); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	items, err := d.Assets().QueryDSLInScope("", "ip", task.ID, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, asset := range items {
		got[asset.IP] = true
	}
	if len(got) != 3 || !got["2001:db8::"] || !got["2001:db8::3"] || !got["198.51.100.9"] {
		t.Fatalf("CIDR boundary membership: %v", got)
	}
	var family, width int
	if err := d.QueryRow(`SELECT ip_family,length(ip_address) FROM assets WHERE type='ip' AND ip='198.51.100.9'`).Scan(&family, &width); err != nil {
		t.Fatal(err)
	}
	if family != 4 || width != 4 {
		t.Fatalf("mapped IPv4 canonical address: family=%d bytes=%d", family, width)
	}
	if err := d.QueryRow(`SELECT net_family,length(net_first) FROM task_scope WHERE task_id=?1 AND net='2001:db8::/126'`, task.ID).Scan(&family, &width); err != nil {
		t.Fatal(err)
	}
	if family != 6 || width != 16 {
		t.Fatalf("IPv6 stored range: family=%d bytes=%d", family, width)
	}
}

func TestSQLiteAssetRelationsRollbackOnStorageFailure(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	assets := d.Assets()
	id, err := assets.UpsertHTTPService(UpsertHTTPServiceReq{URL: "https://fixture.example/", PageTitle: "retained", Technologies: []string{"existing"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`CREATE TRIGGER reject_technology BEFORE INSERT ON asset_technologies WHEN NEW.technology='injected' BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	updated, err := assets.UpsertHTTPService(UpsertHTTPServiceReq{URL: "https://fixture.example/", PageTitle: "must roll back", Technologies: []string{"injected"}})
	if err == nil || updated != 0 {
		t.Fatalf("partial update reported success: id=%d err=%v", updated, err)
	}
	items, err := assets.GetByIDs([]int64{id})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].PageTitle != "retained" || len(items[0].Technologies) != 1 || items[0].Technologies[0] != "existing" {
		t.Fatalf("partial asset mutation committed: %+v", items)
	}
}
