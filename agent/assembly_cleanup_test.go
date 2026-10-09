package agent

import (
	"context"
	"testing"

	actool "github.com/Autumn-27/norma/tool"
)

type ownedToolFixture struct {
	actool.CoreTool
	closes *int
}

func (t ownedToolFixture) Close() error { *t.closes++; return nil }

func TestAugmentToolsClosesHostOwnershipOnce(t *testing.T) {
	previousAugment, previousResolve := ToolAugment, ToolResolve
	t.Cleanup(func() { ToolAugment, ToolResolve = previousAugment, previousResolve })
	owned, augmented := 0, 0
	ToolAugment = func(context.Context, string) ([]actool.CoreTool, DeferredInfo, func()) {
		return nil, DeferredInfo{}, func() { augmented++ }
	}
	ToolResolve = func(_ context.Context, _ string, tools []actool.CoreTool) []actool.CoreTool {
		return []actool.CoreTool{ownedToolFixture{CoreTool: actool.NewBash(), closes: &owned}}
	}
	_, _, cleanup := AugmentTools(context.Background(), "worker", nil)
	cleanup()
	cleanup()
	if owned != 1 || augmented != 1 {
		t.Fatalf("cleanup ownership=%d augmentation=%d", owned, augmented)
	}
}
