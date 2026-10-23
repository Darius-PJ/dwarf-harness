package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"math"
	"slices"
	"unicode/utf8"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

type scopedCaller struct {
	actor   string
	binding Binding
	fixed   submissionIDs
}
type scopedInput struct {
	id     uuid.UUID
	sub    ledger.ScopedSubmission
	digest ledger.Digest
	body   []byte
}

// BindAgent is a trusted host entrance, not authentication of model text. The
// host must authenticate the connection before selecting its registered binding.
func (s *Service) BindAgent(ctx context.Context, binding Binding) (*Agent, error) {
	if s == nil {
		return nil, ErrNotBound
	}
	if err := s.enter(ctx); err != nil {
		return nil, err
	}
	defer s.active.Done()
	ids, err := s.initialIDs(ctx, binding, false)
	if err != nil {
		return nil, err
	}
	return &Agent{service: s, ids: ids}, nil
}

func (s *Service) IssueGrant(ctx context.Context, origin Binding, request GrantIssue) (ScopedReceipt, error) {
	return s.executeScoped(ctx, scopedCaller{actor: "owner", binding: origin}, "grant.issue", request)
}
func (a *Agent) IssueGrant(ctx context.Context, request GrantIssue) (ScopedReceipt, error) {
	return a.command(ctx, "grant.issue", request)
}
func (s *Service) RevokeGrant(ctx context.Context, origin Binding, request GrantRevocation) (ScopedReceipt, error) {
	return s.executeScoped(ctx, scopedCaller{actor: "owner", binding: origin}, "grant.revoke", request)
}
func (a *Agent) RevokeGrant(ctx context.Context, request GrantRevocation) (ScopedReceipt, error) {
	return a.command(ctx, "grant.revoke", request)
}
func (s *Service) ConfigureCapacity(ctx context.Context, origin Binding, request CapacitySpec) (ScopedReceipt, error) {
	return s.executeScoped(ctx, scopedCaller{actor: "owner", binding: origin}, "capacity.configure", request)
}
func (s *Service) CreateTask(ctx context.Context, origin Binding, request TaskCreation) (ScopedReceipt, error) {
	return s.executeScoped(ctx, scopedCaller{actor: "owner", binding: origin}, "task.create", request)
}
func (a *Agent) CreateTask(ctx context.Context, request TaskCreation) (ScopedReceipt, error) {
	return a.command(ctx, "task.create", request)
}
func (s *Service) ClaimTask(ctx context.Context, origin Binding, request TaskAssignment) (ScopedReceipt, error) {
	return s.executeScoped(ctx, scopedCaller{actor: "owner", binding: origin}, "task.claim", request)
}
func (a *Agent) ClaimTask(ctx context.Context, request TaskAssignment) (ScopedReceipt, error) {
	return a.command(ctx, "task.claim", request)
}
func (s *Service) AssignTask(ctx context.Context, origin Binding, request TaskAssignment) (ScopedReceipt, error) {
	return s.executeScoped(ctx, scopedCaller{actor: "owner", binding: origin}, "task.assign", request)
}
func (a *Agent) AssignTask(ctx context.Context, request TaskAssignment) (ScopedReceipt, error) {
	return a.command(ctx, "task.assign", request)
}
func (s *Service) ReassignTask(ctx context.Context, origin Binding, request TaskAssignment) (ScopedReceipt, error) {
	return s.executeScoped(ctx, scopedCaller{actor: "owner", binding: origin}, "task.reassign", request)
}
func (a *Agent) ReassignTask(ctx context.Context, request TaskAssignment) (ScopedReceipt, error) {
	return a.command(ctx, "task.reassign", request)
}
func (s *Service) TransitionTask(ctx context.Context, origin Binding, request TaskMutation) (ScopedReceipt, error) {
	return s.executeScoped(ctx, scopedCaller{actor: "owner", binding: origin}, "task."+request.Action, request)
}
func (a *Agent) TransitionTask(ctx context.Context, request TaskMutation) (ScopedReceipt, error) {
	return a.command(ctx, "task."+request.Action, request)
}

func (a *Agent) command(ctx context.Context, operation string, request any) (ScopedReceipt, error) {
	if a == nil || a.service == nil {
		return ScopedReceipt{}, ErrNotBound
	}
	return a.service.executeScoped(ctx, scopedCaller{actor: "agent", binding: Binding{a.ids.agent, a.ids.context}, fixed: a.ids}, operation, request)
}

func expectedTask(version uint64, assignment uuid.UUID, generation int64) (ledger.ScopedExpected, error) {
	if version >= math.MaxInt64 || assignment == (uuid.UUID{}) && generation != 0 || assignment != (uuid.UUID{}) && generation <= 0 {
		return ledger.ScopedExpected{}, ErrInvalid
	}
	expected := ledger.ScopedExpected{Version: int64(version), AssignmentID: assignment}
	if assignment != (uuid.UUID{}) {
		expected.Generation = &generation
	}
	return expected, nil
}

func normalizeIDs(ids []uuid.UUID) []uuid.UUID {
	copy := append([]uuid.UUID{}, ids...)
	slices.SortFunc(copy, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	return copy
}

// Each array element takes at least one encoded byte. This allocation fence is
// the frozen payload bound, not a configurable producer ceiling; retries remain
// recognizable after an operator lowers Limits.
func boundedArrays(lengths ...int) bool {
	total := 0
	for _, length := range lengths {
		if length > ledger.MaxScopedPayloadBytes-total {
			return false
		}
		total += length
	}
	return true
}

func normalizeGrant(spec GrantSpec) (ledger.GrantIssueArguments, error) {
	if !boundedArrays(len(spec.Operations), len(spec.Resources), len(spec.Recipients)) {
		return ledger.GrantIssueArguments{}, ErrTooLarge
	}
	operations := slices.Clone(spec.Operations)
	slices.Sort(operations)
	resources := slices.Clone(spec.Resources)
	for i := range resources {
		if !boundedArrays(len(resources[i].Slots)) {
			return ledger.GrantIssueArguments{}, ErrTooLarge
		}
		if resources[i].Kind == "context" {
			resources[i].Slots = append([]string{}, resources[i].Slots...)
			slices.Sort(resources[i].Slots)
		}
		if !validResource(resources[i]) {
			return ledger.GrantIssueArguments{}, ErrInvalid
		}
	}
	slices.SortFunc(resources, compareResource)
	recipients := make([]ledger.Binding, len(spec.Recipients))
	for i, binding := range spec.Recipients {
		recipients[i] = ledger.Binding(binding)
	}
	slices.SortFunc(recipients, func(a, b ledger.Binding) int { return compareBinding(Binding(a), Binding(b)) })
	return ledger.GrantIssueArguments{GrantID: spec.GrantID, Grantee: ledger.Binding(spec.Grantee), ParentGrantID: spec.ParentGrantID, Operations: operations, Resources: resources, DataClass: spec.DataClass, Recipients: recipients, Limits: spec.Limits, DelegationDepth: spec.DelegationDepth, Lifetime: spec.Lifetime, TaskBinding: spec.TaskBinding}, nil
}

func makeScopedInput(actor string, ids submissionIDs, operation string, request any) (scopedInput, error) {
	input := scopedInput{sub: ledger.ScopedSubmission{Schema: 2, Actor: actor, Origin: originOf(ids), Operation: operation}}
	var err error
	switch request := request.(type) {
	case GrantIssue:
		if operation != "grant.issue" || request.ExpectedAuthorityVersion >= math.MaxInt64 {
			return input, ErrInvalid
		}
		args, err := normalizeGrant(request.Spec)
		if err != nil {
			return input, err
		}
		if len(args.Resources) == 0 {
			return input, ErrInvalid
		}
		authority := int64(request.ExpectedAuthorityVersion)
		input.id, input.sub.GrantID, input.sub.Arguments = request.CommandID, request.GrantID, args
		input.sub.Target, input.sub.Expected = args.Resources[0], ledger.ScopedExpected{AuthorityVersion: &authority}
	case GrantRevocation:
		if operation != "grant.revoke" || request.ExpectedVersion >= math.MaxInt64 || request.ExpectedAuthorityVersion >= math.MaxInt64 {
			return input, ErrInvalid
		}
		authority := int64(request.ExpectedAuthorityVersion)
		input.id, input.sub.GrantID = request.CommandID, request.GrantID
		input.sub.Expected = ledger.ScopedExpected{Version: int64(request.ExpectedVersion), AuthorityVersion: &authority}
		input.sub.Arguments = ledger.GrantRevokeArguments{GrantID: request.RevokeGrantID}
	case CapacitySpec:
		if operation != "capacity.configure" || actor != "owner" || request.ExpectedVersion >= math.MaxInt64 {
			return input, ErrInvalid
		}
		input.id, input.sub.Expected.Version = request.CommandID, int64(request.ExpectedVersion)
		input.sub.Arguments = ledger.CapacityArguments{Scope: request.Scope, Limits: request.Limits}
	case TaskCreation:
		if operation != "task.create" || request.ExpectedVersion != 0 || !boundedArrays(len(request.Dependencies), len(request.Audience)) {
			return input, ErrInvalid
		}
		if int64(len(request.Description)) > ledgerstore.MaxBodyBytes {
			return input, ErrTooLarge
		}
		if !utf8.Valid(request.Description) {
			return input, ErrInvalid
		}
		input.id, input.sub.GrantID, input.body = request.CommandID, request.GrantID, request.Description
		input.sub.Target = ledger.Resource{Kind: "task", TaskID: request.TaskID}
		input.sub.Arguments = ledger.TaskCreateArguments{TaskID: request.TaskID, Description: ledger.ObjectRef{SHA256: ledger.Digest(sha256.Sum256(request.Description)), Bytes: int64(len(request.Description))}, AcceptanceSHA256: request.AcceptanceSHA256, Dependencies: normalizeIDs(request.Dependencies), Audience: normalizeIDs(request.Audience), Assignment: request.Assignment}
	case TaskAssignment:
		if operation != "task.claim" && operation != "task.assign" && operation != "task.reassign" {
			return input, ErrInvalid
		}
		input.id, input.sub.GrantID = request.CommandID, request.GrantID
		input.sub.Target = ledger.Resource{Kind: "task", TaskID: request.TaskID}
		input.sub.Expected, err = expectedTask(request.ExpectedVersion, request.ExpectedAssignmentID, request.ExpectedGeneration)
		input.sub.Arguments = ledger.TaskMutationArguments{TaskID: request.TaskID, Action: operation[len("task."):], NewAssignment: &request.Assignment}
	case TaskMutation:
		if !slices.Contains([]string{"start", "wait", "resume", "submit", "complete", "fail", "cancel"}, request.Action) || operation != "task."+request.Action {
			return input, ErrInvalid
		}
		input.id, input.sub.GrantID = request.CommandID, request.GrantID
		input.sub.Target = ledger.Resource{Kind: "task", TaskID: request.TaskID}
		input.sub.Expected, err = expectedTask(request.ExpectedVersion, request.ExpectedAssignmentID, request.ExpectedGeneration)
		input.sub.Arguments = ledger.TaskMutationArguments{TaskID: request.TaskID, Action: request.Action, WaitReason: request.WaitReason, WaitArtifact: request.WaitArtifact, Submission: request.Submission, FailureCause: request.FailureCause, FailureEvidence: request.FailureEvidence}
	default:
		return input, ErrInvalid
	}
	if err != nil {
		return input, err
	}
	if !validID(input.id) {
		return input, ErrInvalid
	}
	return input, nil
}

func (s *Service) resolveScopedTarget(ctx context.Context, sub *ledger.ScopedSubmission) error {
	switch args := sub.Arguments.(type) {
	case ledger.GrantRevokeArguments:
		if sub.Actor == "agent" {
			chain, err := s.currentChain(ctx, sub.Origin, sub.GrantID, s.p.through)
			if err != nil {
				return err
			}
			if !slices.Contains(chain[0].operations, "grant.revoke") {
				return ErrDenied
			}
		}
		grant, found, err := s.loadGrant(ctx, args.GrantID, s.p.through)
		if err != nil {
			return err
		}
		if sub.Actor == "agent" && (!found || grant.issuer != "agent" || grant.origin != sub.Origin) {
			return ErrDenied
		}
		if !found {
			return ErrUnavailable
		}
		sub.Target = grant.definition.Resources[0]
	case ledger.CapacityArguments:
		binding := ledger.Binding(bindingOf(sub.Origin))
		if args.Scope.Kind == "context" {
			owner, found := s.p.contexts[args.Scope.ID]
			if !found {
				return ErrNotBound
			}
			binding = ledger.Binding{AgentID: owner, ContextID: args.Scope.ID}
		}
		sub.Target = ledger.Resource{Kind: "context", Binding: binding, Scope: "context", Slots: []string{}}
	}
	return nil
}
