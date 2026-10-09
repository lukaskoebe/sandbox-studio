package runtime

import (
	"errors"
	"testing"
)

func TestCheckpointRemovalRefusesParentBeforeMovingHead(t *testing.T) {
	const group = "sbx-sandbox"
	records := []checkpointRecord{
		{id: "older", group: group},
		{id: "newer", group: group, parentID: "older"},
	}

	if _, err := checkpointRemovalParent(records, group, "older"); !errors.Is(err, ErrCheckpointInUse) {
		t.Fatalf("checkpointRemovalParent error = %v; want ErrCheckpointInUse", err)
	}
}

func TestCheckpointRemovalErrorClassifiesChildAddedAfterPreflight(t *testing.T) {
	const group = "sbx-sandbox"
	removeErr := errors.New("SDK refused removal because an indexed child exists")
	records := []checkpointRecord{
		{id: "older", group: group},
		{id: "newer", group: group, parentID: "older"},
	}

	err := checkpointRemovalError(records, group, "older", removeErr)
	if !errors.Is(err, ErrCheckpointInUse) {
		t.Fatalf("checkpointRemovalError = %v; want ErrCheckpointInUse", err)
	}
	if got := err.Error(); got != ErrCheckpointInUse.Error()+": \"older\"" {
		t.Fatalf("checkpointRemovalError message = %q; want actionable dependency message", got)
	}
}

func TestCheckpointRemovalHeadPrefersParentID(t *testing.T) {
	const group = "sbx-sandbox"
	records := []checkpointRecord{
		{id: "parent-id", group: group},
		{id: "checkpoint-id", group: group, parentID: "parent-id"},
		{id: "other-sandbox-checkpoint", group: "sbx-other"},
	}

	parentID, err := checkpointRemovalParent(records, group, "checkpoint-id")
	if err != nil {
		t.Fatalf("checkpointRemovalParent: %v", err)
	}
	if got := checkpointRemovalHead(records, group, "checkpoint-id", parentID, "checkpoint-id"); got != "parent-id" {
		t.Fatalf("checkpointRemovalHead = %q; want parent snapshot ID %q", got, "parent-id")
	}
}

func TestCheckpointRemovalHeadFallsBackOnlyWithinOwnedGroup(t *testing.T) {
	const group = "sbx-sandbox"
	records := []checkpointRecord{
		{id: "old-head-id", group: group, parentID: "missing-parent-id"},
		{id: "owned-survivor", group: group},
		{id: "foreign-member", group: "sbx-other"},
	}

	if got := checkpointRemovalHead(records, group, "old-head-id", "missing-parent-id", "old-head-id"); got != "owned-survivor" {
		t.Fatalf("checkpointRemovalHead = %q; want same-group member %q", got, "owned-survivor")
	}
}

func TestCheckpointRemovalHeadLeavesSingletonForSDKToClear(t *testing.T) {
	const group = "sbx-sandbox"
	records := []checkpointRecord{{id: "only-member", group: group}}

	if got := checkpointRemovalHead(records, group, "only-member", "", "only-member"); got != "" {
		t.Fatalf("checkpointRemovalHead = %q; want empty head selection for singleton", got)
	}
}

func TestCheckpointRemovalDoesNotMoveAnUnrelatedGroupHead(t *testing.T) {
	const group = "sbx-sandbox"
	records := []checkpointRecord{
		{id: "target", group: group},
		{id: "same-group-member", group: group},
	}

	if got := checkpointRemovalHead(records, group, "target", "", "same-group-member"); got != "" {
		t.Fatalf("checkpointRemovalHead = %q; want no head change", got)
	}
}
