package agents

import (
	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
)

func paidDescriptor(d ledger.SealedDescriptor) providers.Descriptor {
	out := providers.Descriptor{Provider: d.Provider, Authority: d.Authority, Model: d.Model, Region: d.Region, Method: d.Method, Path: d.Path, RequestedTier: d.RequestedTier, CredentialScope: d.Credential.ScopeID.String(), CredentialVersion: d.Credential.Version.String()}
	for _, h := range d.Headers {
		out.Headers = append(out.Headers, providers.Header{Name: h.Name, Value: h.Value})
	}
	return out
}

// Only an explicit status request enters this bounded byte-reading path. A
// missing or changed object cannot erase a ledger hold, settlement or fence.
func (s *Service) verifyPaidInput(a paidAttempt) error {
	if s.deps.beforeRead != nil {
		if err := s.deps.beforeRead(bindingOf(a.origin), "paid_input"); err != nil {
			return err
		}
	}
	scope, err := newPrivateScope(s.colony, s.p.agents[a.origin.AgentID].directory, a.origin.ContextID.String(), &s.dirs)
	if err != nil {
		return err
	}
	ref := a.admission.InputObject
	body, err := readObject(scope, Head{SHA256: ref.SHA256, Bytes: ref.Bytes})
	if err != nil {
		return err
	}
	if ledger.Digest(providers.RequestSHA256(paidDescriptor(a.admission.Request), body)) != a.admission.Quote.RequestSHA256 {
		return ErrIntegrity
	}
	return nil
}
