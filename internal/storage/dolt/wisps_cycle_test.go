package dolt

import (
	"context"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestAddDependencyRejectsPermanentEndpointCycleThroughWisp(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	const (
		permA = "cycle-perm-a"
		permX = "cycle-perm-x"
		wispW = "cycle-wisp-w"
	)
	createPerm(t, ctx, store, permA)
	createPerm(t, ctx, store, permX)
	createWisp(t, ctx, store, wispW)

	mustAddBlockingDependency(t, ctx, store, permX, wispW)
	mustAddBlockingDependency(t, ctx, store, wispW, permA)

	err := store.AddDependency(ctx, &types.Dependency{
		IssueID:     permA,
		DependsOnID: permX,
		Type:        types.DepBlocks,
	}, "tester")
	assertCycleError(t, err)
}

func TestAddDependencyRejectsWispEndpointCycleThroughPermanent(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	const (
		wispA = "cycle-wisp-a"
		wispX = "cycle-wisp-x"
		permB = "cycle-perm-b"
	)
	createWisp(t, ctx, store, wispA)
	createWisp(t, ctx, store, wispX)
	createPerm(t, ctx, store, permB)

	mustAddBlockingDependency(t, ctx, store, wispX, permB)
	mustAddBlockingDependency(t, ctx, store, permB, wispA)

	err := store.AddDependency(ctx, &types.Dependency{
		IssueID:     wispA,
		DependsOnID: wispX,
		Type:        types.DepBlocks,
	}, "tester")
	assertCycleError(t, err)
}

func TestAddDependencyAcceptsCrossTableDiamond(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	const (
		permTop  = "diamond-perm-top"
		wispLeft = "diamond-wisp-left"
		permMid  = "diamond-perm-mid"
		permEnd  = "diamond-perm-end"
	)
	createPerm(t, ctx, store, permTop)
	createWisp(t, ctx, store, wispLeft)
	createPerm(t, ctx, store, permMid)
	createPerm(t, ctx, store, permEnd)

	// Two distinct paths top -> end, one of them through the wisp table.
	mustAddBlockingDependency(t, ctx, store, permTop, wispLeft)
	mustAddBlockingDependency(t, ctx, store, wispLeft, permEnd)
	mustAddBlockingDependency(t, ctx, store, permTop, permMid)

	// Closing the diamond is not a cycle and must be accepted.
	mustAddBlockingDependency(t, ctx, store, permMid, permEnd)
}

func TestAddDependencyRejectsSelfDependency(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	const solo = "self-dep-perm"
	createPerm(t, ctx, store, solo)

	err := store.AddDependency(ctx, &types.Dependency{
		IssueID:     solo,
		DependsOnID: solo,
		Type:        types.DepBlocks,
	}, "tester")
	if err == nil {
		t.Fatal("expected AddDependency to reject a self-dependency, but it succeeded")
	}
	if !strings.Contains(err.Error(), "cannot depend on itself") {
		t.Fatalf("expected self-dependency error, got: %v", err)
	}
}

func mustAddBlockingDependency(t *testing.T, ctx context.Context, store *DoltStore, issueID, dependsOnID string) {
	t.Helper()
	if err := store.AddDependency(ctx, &types.Dependency{
		IssueID:     issueID,
		DependsOnID: dependsOnID,
		Type:        types.DepBlocks,
	}, "tester"); err != nil {
		t.Fatalf("AddDependency %s->%s: %v", issueID, dependsOnID, err)
	}
}

func assertCycleError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected AddDependency to reject mixed-table cycle, but it succeeded")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got: %v", err)
	}
}
