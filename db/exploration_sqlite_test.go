package db

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
)

func TestSQLiteExplorationReopenPreservesCapturedTextAndAtomicClaims(t *testing.T) {
	filename := testDSN(t)
	d, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	task, err := d.CreateTask("로컬 탐색", "fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	store := d.Exploration(task.ExplorationID)
	nodeID, err := store.AddIntent(map[string]any{"summary": "Ärtex\x00증거"}, 1, nil, "human")
	if err != nil {
		t.Fatal(err)
	}
	metadata := json.RawMessage(`{"body":"ab\u0000cd"}`)
	activityID, err := store.AppendActivity(Activity{NodeID: &nodeID, Summary: "Ärtex\x00증거", Detail: "ab\x00cd\xff", Metadata: metadata})
	if err != nil {
		t.Fatal(err)
	}
	if id, err := store.AddIntent(map[string]any{"invalid": make(chan int)}, 1, nil, "human"); err == nil || id != 0 {
		t.Fatalf("invalid JSON accepted: id=%d err=%v", id, err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	store = d.Exploration(task.ExplorationID)
	node, err := store.GetNode(nodeID)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]string
	if err := json.Unmarshal(node.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["summary"] != "Ärtex\x00증거" {
		t.Fatalf("payload content changed: %q", payload["summary"])
	}
	detail, err := store.ActivityDetail(activityID)
	if err != nil || detail != "ab\x00cd�" {
		t.Fatalf("captured text=%q err=%v", detail, err)
	}
	items, _, err := store.ActivityList(nil, 0, 10)
	if err != nil || len(items) != 1 || string(items[0].Metadata) != string(metadata) {
		t.Fatalf("metadata round trip: %+v err=%v", items, err)
	}
	nodes, total, err := store.NodesPage(NodeFilter{Query: "ärtex"}, 1, 10)
	if err != nil || total != 1 || len(nodes) != 1 || nodes[0].ID != nodeID {
		t.Fatalf("Unicode payload search: total=%d nodes=%v err=%v", total, nodes, err)
	}
	var winners atomic.Int32
	var wg sync.WaitGroup
	errors := make(chan error, 12)
	for range 12 {
		wg.Go(func() {
			ok, err := store.ClaimIntent(nodeID, "worker")
			if err != nil {
				errors <- err
			}
			if ok {
				winners.Add(1)
			}
		})
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	if winners.Load() != 1 {
		t.Fatalf("claim winners=%d", winners.Load())
	}
}
