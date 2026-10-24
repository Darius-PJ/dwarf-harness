package agents

import (
	"bytes"
	"errors"
	"slices"
	"uuid"
)

// ErrReviewRequired refuses a direct terminal transition while an exact-task
// review obligation remains pending or rejected, including for the owner.
var ErrReviewRequired = errors.New("agents: review required")

// A live task retains at most one slot. The caller creates its pending value
// from a validated request; these rules do not install a slot in the service.
// Binding metadata is immutable throughout the slot's lifecycle.
type reviewSlot struct {
	id           uuid.UUID
	binding      reviewBinding
	kind, state  string
	supersededBy uuid.UUID
}

// transitionReviewSlot changes no input, and every refusal returns the input.
// Supersession names the old ID explicitly and a different new ID. The caller
// proves the new ID unused and installs the replacement's own pending slot.
// Staleness records a task cancellation, reassignment or version change; it
// cannot be reversed merely because a later task has equal hashes or values.
func transitionReviewSlot(current reviewSlot, request uuid.UUID, action string, replacement uuid.UUID) (reviewSlot, error) {
	if current.id != request || !validID(current.id) ||
		(current.state != "pending" && current.state != "rejected") ||
		(current.kind != "review" && current.kind != "failure") ||
		action != "supersede" && replacement != (uuid.UUID{}) {
		return current, ErrInvalid
	}
	next := current
	switch action {
	case "approve", "reject":
		if current.state != "pending" || current.kind != "review" {
			return current, ErrInvalid
		}
		if action == "approve" {
			next.state = "approved"
		} else {
			next.state = "rejected"
		}
	case "fail":
		if current.state != "pending" || current.kind != "failure" {
			return current, ErrInvalid
		}
		next.state = "failed"
	case "supersede":
		if !validID(replacement) || replacement == current.id {
			return current, ErrInvalid
		}
		next.state, next.supersededBy = "superseded", replacement
	case "stale":
		next.state = "stale"
	default:
		return current, ErrInvalid
	}
	return next, nil
}

// guardReviewTask is the pure predicate for planTask's pre-kernel seam. No
// caller principal or wall clock can exempt a pending or rejected obligation.
// S3a will connect it when the reducer can actually consume review slots.
func guardReviewTask(slot reviewSlot, current taskState, action string) error {
	if action != "complete" && action != "fail" {
		return nil
	}
	if (slot.state == "pending" || slot.state == "rejected") &&
		slot.binding.task == current.id && slot.binding.version == current.version &&
		slot.binding.assignment == current.assignment &&
		!slices.Contains([]string{"completed", "failed", "cancelled"}, current.state) {
		return ErrReviewRequired
	}
	return nil
}

func reviewRequestNeedsScan(id, maxRequest uuid.UUID) bool {
	return bytes.Compare(id[:], maxRequest[:]) <= 0
}
