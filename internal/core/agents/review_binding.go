package agents

import (
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// These record-free values describe already validated review inputs, not a
// wire format. A subject names one occurrence, never just its content digest.
type reviewSubject struct {
	kind                     string
	binding                  Binding
	scope, slot              string
	revision, sourceSequence uint64
	sha256                   ledger.Digest
	bytes                    int64
}

type reviewPrincipal struct {
	actor  string
	origin ledger.Origin
	grant  uuid.UUID
}

type reviewBinding struct {
	task                      uuid.UUID
	version                   uint64
	assignment                taskAssignment
	reviewer                  reviewPrincipal
	subject                   reviewSubject
	acceptance, check, config ledger.Digest
	evidence                  []reviewSubject
	authorizationRevision     int64
	deadline                  int64
}

func matchReviewBinding(want, got reviewBinding) error {
	if want.task != got.task || want.version != got.version || want.assignment != got.assignment ||
		want.reviewer != got.reviewer || want.subject != got.subject ||
		want.acceptance != got.acceptance || want.check != got.check || want.config != got.config ||
		!slices.Equal(want.evidence, got.evidence) || want.authorizationRevision != got.authorizationRevision ||
		want.deadline != got.deadline {
		return ErrInvalid
	}
	return nil
}
