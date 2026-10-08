package db

import (
	"fmt"
	"testing"
	"time"
)

func TestDeleteTaskTrafficHostsUseOneLockedTransaction(t *testing.T) {
	dsn := testDSN(t)
	d, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	t.Run("sharing committed before deletion lock is observed", func(t *testing.T) {
		first, second, host, rootAssetID := createTaskDeleteRaceFixture(t, d)
		serviceURL := "https://" + host + "/concurrent-owner"
		t.Cleanup(func() {
			_ = d.DeleteTask(first.ID)
			_ = d.DeleteTask(second.ID)
			_, _ = d.Exec(`DELETE FROM assets WHERE id=$1 OR url=$2`, rootAssetID, serviceURL)
		})

		writer := openTaskDeleteTestDB(t, dsn)
		deleter := openTaskDeleteTestDB(t, dsn)
		writerTx, err := writer.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer writerTx.Rollback()
		if _, err := (&AssetStore{db: writer, company: writer.Companies(), tx: writerTx}).UpsertHTTPService(UpsertHTTPServiceReq{URL: serviceURL, TaskID: second.ID}); err != nil {
			t.Fatal(err)
		}

		var preparedHosts []string
		deleteDone := make(chan error, 1)
		go func() {
			_, deleteErr := deleter.DeleteTaskCascadePrepared(first.ID, true, false, false, func(p TaskDeletePreparation) error {
				preparedHosts = append([]string(nil), p.TrafficHosts...)
				return nil
			})
			deleteDone <- deleteErr
		}()

		if err := waitForTaskDeleteBlock(deleteDone); err != nil {
			t.Fatal(err)
		}
		if err := writerTx.Commit(); err != nil {
			t.Fatal(err)
		}
		if err := waitForTaskDeleteResult(deleteDone); err != nil {
			t.Fatal(err)
		}
		if containsDeleteHost(preparedHosts, host) {
			t.Fatalf("newly shared host %q was selected for traffic deletion: %v", host, preparedHosts)
		}
		var remaining int
		if err := d.QueryRow(`SELECT count(*) FROM assets a WHERE url=$1 AND EXISTS(SELECT 1 FROM task_asset_links link WHERE link.asset_id=a.id AND link.task_id=$2)`, serviceURL, second.ID).Scan(&remaining); err != nil {
			t.Fatal(err)
		}
		if remaining != 1 {
			t.Fatalf("concurrent owner's asset was not preserved: count=%d", remaining)
		}
	})

	t.Run("sharing cannot enter between host resolution and commit", func(t *testing.T) {
		first, second, host, rootAssetID := createTaskDeleteRaceFixture(t, d)
		serviceURL := "https://" + host + "/late-owner"
		t.Cleanup(func() {
			_ = d.DeleteTask(first.ID)
			_ = d.DeleteTask(second.ID)
			_, _ = d.Exec(`DELETE FROM assets WHERE id=$1 OR url=$2`, rootAssetID, serviceURL)
		})

		deleter := openTaskDeleteTestDB(t, dsn)
		writer := openTaskDeleteTestDB(t, dsn)
		prepared := make(chan TaskDeletePreparation, 1)
		releasePrepare := make(chan struct{})
		deleteDone := make(chan error, 1)
		go func() {
			_, deleteErr := deleter.DeleteTaskCascadePrepared(first.ID, true, false, false, func(p TaskDeletePreparation) error {
				prepared <- p
				<-releasePrepare
				return nil
			})
			deleteDone <- deleteErr
		}()

		var plan TaskDeletePreparation
		select {
		case plan = <-prepared:
		case err := <-deleteDone:
			close(releasePrepare)
			t.Fatalf("delete returned before preparation: %v", err)
		case <-time.After(5 * time.Second):
			close(releasePrepare)
			t.Fatal("timed out waiting for task delete preparation")
		}
		if !containsDeleteHost(plan.TrafficHosts, host) {
			close(releasePrepare)
			t.Fatalf("exclusive host %q missing from preparation: %v", host, plan.TrafficHosts)
		}

		writerDone := make(chan error, 1)
		go func() {
			_, writeErr := writer.Assets().UpsertHTTPService(UpsertHTTPServiceReq{URL: serviceURL, TaskID: second.ID})
			writerDone <- writeErr
		}()
		if err := waitForTaskDeleteBlock(writerDone); err != nil {
			close(releasePrepare)
			t.Fatal(err)
		}

		close(releasePrepare)
		if err := waitForTaskDeleteResult(deleteDone); err != nil {
			t.Fatal(err)
		}
		if err := waitForTaskDeleteResult(writerDone); err != nil {
			t.Fatal(err)
		}
	})
}

func createTaskDeleteRaceFixture(t *testing.T, d *DB) (first, second *Task, host string, rootAssetID int64) {
	t.Helper()
	var err error
	first, err = d.CreateTask("delete race owner", "delete safely", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err = d.CreateTask("delete race sharer", "preserve shared host", nil, 0, 0)
	if err != nil {
		_ = d.DeleteTask(first.ID)
		t.Fatal(err)
	}
	host = fmt.Sprintf("task-delete-race-%d.example.test", first.ID)
	rootAssetID, err = d.Assets().UpsertRootDomain(UpsertRootDomainReq{Domain: host, TaskID: first.ID})
	if err != nil {
		_ = d.DeleteTask(first.ID)
		_ = d.DeleteTask(second.ID)
		t.Fatal(err)
	}
	return first, second, host, rootAssetID
}

func openTaskDeleteTestDB(t *testing.T, filename string) *DB {
	t.Helper()
	d, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// The held IMMEDIATE transaction must prevent another writer from finishing.
// The subsequent committed result also proves the waiting writer reads fresh data.
func waitForTaskDeleteBlock(done <-chan error) error {
	select {
	case err := <-done:
		return fmt.Errorf("operation escaped the held SQLite writer transaction: %v", err)
	case <-time.After(100 * time.Millisecond):
		return nil
	}
}

func waitForTaskDeleteResult(done <-chan error) error {
	select {
	case err := <-done:
		return err
	case <-time.After(12 * time.Second):
		return fmt.Errorf("timed out waiting for concurrent task deletion operation")
	}
}

func containsDeleteHost(hosts []string, want string) bool {
	for _, host := range hosts {
		if host == want {
			return true
		}
	}
	return false
}
