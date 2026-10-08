package db

import (
	"sync"
	"testing"
)

func TestSQLiteCompanyConcurrentUpsertHasSingleCreator(t *testing.T) {
	d := openBusinessFixture(t, testDSN(t))
	var wait sync.WaitGroup
	results := make(chan bool, 12)
	failures := make(chan error, 12)
	for range 12 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, created, err := d.Companies().UpsertCompany("동시 기업", "")
			if err != nil {
				failures <- err
			}
			results <- created
		}()
	}
	wait.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	creators := 0
	for created := range results {
		if created {
			creators++
		}
	}
	if creators != 1 {
		t.Fatalf("creators=%d want 1", creators)
	}
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM companies WHERE nkey=?1`, "동시 기업").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("companies=%d want 1", count)
	}
}
