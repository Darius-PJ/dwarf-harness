package agents

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func encodeScopedSubmission(submission ledger.ScopedSubmission) (json.RawMessage, ledger.Digest, error) {
	data, err := submission.Encode()
	if err != nil {
		return nil, ledger.Digest{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return data, ledger.Digest(sha256.Sum256(data)), nil
}
