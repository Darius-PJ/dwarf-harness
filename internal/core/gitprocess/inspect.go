package gitprocess

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"uuid"
)

// InspectFences decodes the existing persistent layout without opening an owner,
// creating a lock, sweeping debris or consulting/terminating native processes.
// Missing initial metadata is normal. Malformed, incomplete or unclosed state
// requires normal startup recovery. Closed records are not a live closure proof.
func InspectFences(ctx context.Context, root string) (Report, error) {
	if err := checkContext(ctx); err != nil {
		return Report{}, err
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return Report{}, err
	}
	if _, err := realDirectory(root); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Report{}, nil
		}
		return Report{}, err
	}
	o := &Owner{root: root, pending: make(map[uuid.UUID]fence)}
	decodeErr := o.loadFencesMode(true)
	report := Report{Reservations: len(o.loadedSlots)}
	for id, fence := range o.pending {
		if fence.State != "closed" {
			report.Pending = append(report.Pending, Pending{RunID: id, Reason: "closure_unconfirmed"})
		}
	}
	sort.Slice(report.Pending, func(a, b int) bool { return report.Pending[a].RunID.String() < report.Pending[b].RunID.String() })
	if decodeErr != nil {
		return report, decodeErr
	}
	if len(report.Pending) != 0 || report.Reservations != 0 {
		return report, ErrUnconfirmed
	}
	return report, ctx.Err()
}
