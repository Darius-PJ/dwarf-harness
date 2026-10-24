package agents

import (
	"slices"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// Compare the frozen nested values directly; a private occurrence includes its
// source and revision, even when its bytes equal another occurrence.
func matchReviewBinding(want, got ledger.ReviewBinding) error {
	if want.TaskID != got.TaskID || want.TaskVersion != got.TaskVersion ||
		want.AssignmentID != got.AssignmentID || want.Generation != got.Generation ||
		want.Assignee != got.Assignee || want.Reviewer != got.Reviewer ||
		!equalSubject(want.Subject, got.Subject) ||
		want.AcceptanceSHA256 != got.AcceptanceSHA256 || want.CheckSHA256 != got.CheckSHA256 ||
		want.ConfigSHA256 != got.ConfigSHA256 ||
		!slices.EqualFunc(want.RequiredEvidence, got.RequiredEvidence, equalSubject) ||
		want.AuthorizationRevision != got.AuthorizationRevision || want.DeadlineUTCNS != got.DeadlineUTCNS {
		return ErrInvalid
	}
	return nil
}
