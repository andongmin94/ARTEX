package db

import (
	"fmt"
	"testing"
)

func TestSQLiteChatMentionsLiteralUnicodeAndStableCursor(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for i := range 25 {
		_, err := d.AddFinding(0, 0, "fixture", fmt.Sprintf("Ärtex 100%%_\\ %d", i), "high", "s", "e", "w", nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{"ärtex", "100%_\\"} {
		first, err := d.SearchChatMentionsPage(t.Context(), "finding", query, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(first.Items) != 20 || first.NextCursor == "" {
			t.Fatalf("first page=%+v", first)
		}
		second, err := d.SearchChatMentionsPage(t.Context(), "finding", query, first.NextCursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(second.Items) != 5 || second.NextCursor != "" {
			t.Fatalf("second page=%+v", second)
		}
		seen := map[int64]bool{}
		for _, item := range append(first.Items, second.Items...) {
			if seen[item.ID] {
				t.Fatalf("duplicate id %d", item.ID)
			}
			seen[item.ID] = true
		}
	}
	exact, err := d.AddFinding(0, 0, "fixture", "exact marker", "high", "s", "e", "w", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.AddFinding(0, 0, "fixture", fmt.Sprintf("text contains id %d", exact), "high", "s", "e", "w", nil)
	if err != nil {
		t.Fatal(err)
	}
	page, err := d.SearchChatMentionsPage(t.Context(), "finding", fmt.Sprint(exact), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) < 2 || page.Items[0].ID != exact {
		t.Fatalf("exact ID priority: %+v", page)
	}
	page, err = d.SearchChatMentionsPage(t.Context(), "finding", "100%X", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("literal wildcard matched: %+v", page)
	}
}
