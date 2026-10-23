package agents

import (
	"context"
	"math"
	"slices"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func (s *Service) planScopedPrivate(ctx context.Context, g *scopedGroup, first uint64) (scopedPlan, []grantRules, int64, error) {
	plan := scopedPlan{authority: s.p.authority}
	sub, through := g.receipt.Submission, first-1
	var size int64
	var chain []grantRules
	var err error
	switch args := sub.Arguments.(type) {
	case ledger.PrivateWriteArguments:
		p := *g.revision
		if p.Scope != sub.Target.Scope || p.ExpectedVersion != sub.Expected.Version || g.receipt.Version != p.Revision {
			return plan, nil, 0, ErrInvalid
		}
		changes := slices.Clone(p.Changes)
		slots := make([]string, len(changes))
		for i := range changes {
			if changes[i].Bytes > math.MaxInt64-size {
				return plan, nil, 0, ErrInvalid
			}
			size += changes[i].Bytes
			changes[i].PreviousSHA256 = ledger.Digest{}
			slots[i] = changes[i].Slot
		}
		if !sameScopedValue(changes, args.Changes) || !slices.Equal(slots, sub.Target.Slots) {
			return plan, nil, 0, ErrInvalid
		}
		chain, err = s.authorize(ctx, sub.Origin, sub.GrantID, "private.write", []ledger.Resource{sub.Target}, nil, size, through)
		if err != nil {
			return plan, nil, 0, err
		}
		plan.private, err = s.p.planPrivateRevisions(g.ids, []ledger.PrivateRevision{{Scope: p.Scope, ExpectedVersion: p.ExpectedVersion, Revision: p.Revision, Changes: p.Changes}}, []int{g.effectIndex}, first)
		if err != nil {
			return plan, nil, 0, err
		}
	case ledger.PrivateReadArguments:
		if g.usage.Read == nil || *g.usage.Read != args.Read || sub.Target.Scope != args.Read.Scope || !slices.Equal(sub.Target.Slots, []string{args.Read.Slot}) || g.receipt.Version != 0 {
			return plan, nil, 0, ErrInvalid
		}
		size = g.usage.Bytes
		chain, err = s.authorize(ctx, sub.Origin, sub.GrantID, "private.read", []ledger.Resource{sub.Target}, nil, size, through)
		if err != nil {
			return plan, nil, 0, err
		}
		if err := s.validatePrivateSelection(ctx, g.ids, *g.usage, through); err != nil {
			return plan, nil, 0, err
		}
	case ledger.ArtifactReadArguments:
		if g.usage.Artifact == nil || *g.usage.Artifact != args.Artifact || sub.Target.Artifact != args.Artifact || g.receipt.Version != 0 {
			return plan, nil, 0, ErrInvalid
		}
		size = args.Artifact.Bytes
		chain, err = s.artifactAuthority(ctx, sub.Origin, args.Artifact, sub.GrantID, through)
		if err != nil {
			return plan, nil, 0, err
		}
		if err := s.verifyArtifactSource(ctx, args.Artifact, through); err != nil {
			return plan, nil, 0, err
		}
	default:
		return plan, nil, 0, ErrInvalid
	}
	return plan, chain, size, nil
}

// Selection is historical metadata, not a body read. A transcript result is an
// exact prefix after a real cursor. Its historical producer's chunk ceiling is
// not replaced by today's Limits, so a bounded early stop is proved by cursor.
func (s *Service) validatePrivateSelection(ctx context.Context, ids submissionIDs, usage ledger.RuntimeLocalUsed, through uint64) error {
	read := *usage.Read
	var head Head
	index, used := 0, int64(0)
	cursorFound, more := read.Cursor == 0, false
	revision, err := s.scopeHistory(ctx, ids, read.Scope, through, uint64(read.Revision), func(slot string, next Head, mode string) error {
		if slot != read.Slot {
			return nil
		}
		if read.Slot != "transcript" {
			head = next
			if mode == "clear" {
				head = Head{}
			}
			return nil
		}
		if !cursorFound {
			if next.Sequence == uint64(read.Cursor) {
				cursorFound = true
			}
			return nil
		}
		if index == len(usage.Selected) {
			more = true
			return nil
		}
		want := ledger.SelectedObject{SHA256: next.SHA256, Bytes: next.Bytes, Revision: int64(next.Revision), Sequence: int64(next.Sequence)}
		if usage.Selected[index] != want || next.Bytes > read.MaxBytes-used {
			return ErrInvalid
		}
		used += next.Bytes
		index++
		return nil
	})
	if err != nil {
		return err
	}
	if revision != uint64(read.Revision) || !cursorFound {
		return ErrInvalid
	}
	if read.Slot != "transcript" {
		if head.SHA256 != (ledger.Digest{}) {
			want := ledger.SelectedObject{SHA256: head.SHA256, Bytes: head.Bytes, Revision: int64(head.Revision), Sequence: int64(head.Sequence)}
			if len(usage.Selected) != 1 || usage.Selected[0] != want || head.Bytes > read.MaxBytes {
				return ErrInvalid
			}
			used, index = head.Bytes, 1
		}
	}
	if index != len(usage.Selected) || used != usage.Bytes {
		return ErrInvalid
	}
	if more {
		if index == 0 || usage.NextCursor == nil || *usage.NextCursor != usage.Selected[index-1].Sequence {
			return ErrInvalid
		}
	} else if usage.NextCursor != nil {
		return ErrInvalid
	}
	return nil
}
