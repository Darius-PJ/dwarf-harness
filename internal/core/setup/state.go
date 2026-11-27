package setup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"reflect"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

var ErrInconsistent = errors.New("setup: inconsistent committed records")

type Context struct {
	AgentID   uuid.UUID
	ContextID uuid.UUID
	Revision  int64
	Settings  ledger.SetupContextArguments
}

type nativeContext struct {
	agent           uuid.UUID
	kind            string
	hasTurn         bool
	provider, model string
}

type State struct {
	ColonyID               uuid.UUID
	ThroughSequence        uint64
	Managed                bool
	Contexts               []Context
	Startup                *ledger.SetupStartupArguments
	StartupRevision        int64
	ChecksRevision         int64
	receipts               map[uuid.UUID]Receipt
	commands               map[uuid.UUID]ledger.Digest
	contexts               map[uuid.UUID]Context
	nativeContexts         map[uuid.UUID]nativeContext
	workspaces             map[uuid.UUID]ledger.CaptureInputsPinned
	workspaceRegistrations map[uuid.UUID]ledger.WorkspaceRegistered
	routes                 map[uuid.UUID]ledger.RuntimePaidRouteConfigured
}

var setupConsumption = ledger.Consumption{
	ledger.TypeCommand: {1}, ledger.TypeRuntimeSetupApplied: {1}, ledger.TypeRuntimeSetupReceipt: {1}, ledger.TypeBodyPin: {1},
	ledger.TypeWorkspaceRegistered: {1}, ledger.TypeCaptureInputsPinned: {1}, ledger.TypeRuntimeAgentRegistered: {1}, ledger.TypeRuntimeContextCreated: {1},
	ledger.TypeRuntimeWorkAdmitted: {1}, ledger.TypeRuntimePaidRouteConfigured: {1},
}

// setupReader is the verified prefix shared by normal startup and inspection.
type setupReader interface {
	LogDir() string
	Colony() uuid.UUID
	Durable() uint64
	ReadBody(context.Context, ledger.Digest) ([]byte, error)
}

// Load reads the acknowledged prefix without opening agents or workspace,
// repairing files, resolving sources or applying incomplete setup operations.
func Load(ctx context.Context, store setupReader) (State, error) {
	if store == nil || reflect.ValueOf(store).Kind() == reflect.Pointer && reflect.ValueOf(store).IsNil() {
		return State{}, fail("storage_unavailable", "", Target{}, nil)
	}
	return load(ctx, store.LogDir(), store.Colony(), store.Durable())
}

func load(ctx context.Context, logDir string, colony uuid.UUID, through uint64) (_ State, err error) {
	s := State{ColonyID: colony, ThroughSequence: through, Contexts: []Context{}, receipts: map[uuid.UUID]Receipt{}, commands: map[uuid.UUID]ledger.Digest{}, contexts: map[uuid.UUID]Context{}, nativeContexts: map[uuid.UUID]nativeContext{}, workspaces: map[uuid.UUID]ledger.CaptureInputsPinned{}, workspaceRegistrations: map[uuid.UUID]ledger.WorkspaceRegistered{}, routes: map[uuid.UUID]ledger.RuntimePaidRouteConfigured{}}
	r, err := ledger.OpenReader(logDir, ledger.ReadOptions{Colony: colony, Through: through, Consume: setupConsumption, AllTypes: true})
	if err != nil {
		return State{}, err
	}
	defer func() { err = errors.Join(err, r.Close()) }()
	for {
		if err := ctx.Err(); err != nil {
			return State{}, err
		}
		c, err := r.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return State{}, err
		}
		isSetup := slices.ContainsFunc(c.Records, func(r ledger.Record) bool {
			return r.Type == ledger.TypeRuntimeSetupApplied || r.Type == ledger.TypeRuntimeSetupReceipt
		})
		if isSetup {
			if err := s.applyGroup(c); err != nil {
				return State{}, err
			}
		} else {
			for _, record := range c.Records {
				if err := s.native(record); err != nil {
					return State{}, err
				}
			}
		}
		for _, record := range c.Records {
			if record.Type == ledger.TypeCommand {
				command, err := ledger.DecodeCommand(record.Payload)
				if err != nil {
					return State{}, err
				}
				if _, exists := s.commands[command.CommandID]; exists {
					return State{}, ErrInconsistent
				}
				s.commands[command.CommandID] = command.SubmissionSHA256
			}
		}
	}
	for _, c := range s.contexts {
		s.Contexts = append(s.Contexts, c)
	}
	slices.SortFunc(s.Contexts, func(a, b Context) int { return bytes.Compare(a.ContextID[:], b.ContextID[:]) })
	return s, nil
}

func (s *State) native(r ledger.Record) error {
	switch r.Type {
	case ledger.TypeRuntimeAgentRegistered:
		_, err := ledger.DecodeRuntimeAgentRegistered(r.Payload)
		return err
	case ledger.TypeRuntimeContextCreated:
		p, err := ledger.DecodeRuntimeContextCreated(r.Payload)
		if err != nil {
			return err
		}
		s.nativeContexts[r.ContextID] = nativeContext{agent: r.AgentID, kind: p.Kind}
	case ledger.TypeWorkspaceRegistered:
		p, err := ledger.DecodeWorkspaceRegistered(r.Payload)
		if err != nil {
			return err
		}
		s.workspaceRegistrations[p.WorkspaceID] = p
	case ledger.TypeCaptureInputsPinned:
		p, err := ledger.DecodeCaptureInputsPinned(r.Payload)
		if err != nil {
			return err
		}
		s.workspaces[p.WorkspaceID] = p
	case ledger.TypeRuntimePaidRouteConfigured:
		p, err := ledger.DecodeRuntimePaidRouteConfigured(r.Payload)
		if err != nil {
			return err
		}
		s.routes[p.RouteID] = p
	case ledger.TypeRuntimeWorkAdmitted:
		p, err := ledger.DecodeRuntimeWorkAdmitted(r.Payload)
		if err != nil {
			return err
		}
		if p.Kind == "turn" {
			n := s.nativeContexts[r.ContextID]
			if !n.hasTurn {
				setting := s.contexts[r.ContextID].Settings
				route := s.routes[setting.RouteID]
				n.hasTurn, n.provider, n.model = true, route.Profile.Provider, route.Profile.ModelID
				s.nativeContexts[r.ContextID] = n
			}
		}
	}
	return nil
}

func (s *State) applyGroup(c ledger.Commit) error {
	if len(c.Records) != 3 && len(c.Records) != 4 {
		return ErrInconsistent
	}
	r := c.Records
	if r[0].Type != ledger.TypeCommand || r[1].Type != ledger.TypeRuntimeSetupApplied || r[len(r)-1].Type != ledger.TypeRuntimeSetupReceipt {
		return ErrInconsistent
	}
	command, err := ledger.DecodeCommand(r[0].Payload)
	if err != nil {
		return err
	}
	applied, err := ledger.DecodeRuntimeSetupApplied(r[1].Payload)
	if err != nil {
		return err
	}
	receipt, err := ledger.DecodeRuntimeSetupReceipt(r[len(r)-1].Payload)
	if err != nil {
		return err
	}
	sub := applied.Submission
	encoded, err := sub.Encode()
	if err != nil {
		return err
	}
	digest := ledger.Digest(sha256.Sum256(encoded))
	if command.SubmissionSHA256 != digest || receipt.SubmissionSHA256 != digest || receipt.CommandID != command.CommandID || receipt.Revision != sub.ExpectedRevision+1 || receipt.Outcome != "applied" {
		return ErrInconsistent
	}
	pin := sub.Operation == "startup.set" || sub.Operation == "checks.publish"
	if pin != (len(r) == 4) {
		return ErrInconsistent
	}
	for i, record := range r {
		if record.Actor != ledger.ActorOwner || record.OperationID != command.CommandID || record.AgentID != (uuid.UUID{}) || record.ExecutionSessionID != (uuid.UUID{}) || record.ContextID != (uuid.UUID{}) || record.TurnID != (uuid.UUID{}) || record.MeasurementPath != "" {
			return ErrInconsistent
		}
		if !pin || i == 0 || i == len(r)-1 {
			if len(record.BodyRefs) != 0 {
				return ErrInconsistent
			}
		}
	}
	if sub.Target.Kind == "colony" && sub.Target.ID != s.ColonyID {
		return ErrInconsistent
	}
	if pin {
		if r[2].Type != ledger.TypeBodyPin || !slices.Equal(r[1].BodyRefs, r[2].BodyRefs) {
			return ErrInconsistent
		}
		if _, err := ledger.DecodeBodyPin(r[2].Payload); err != nil {
			return err
		}
		refs := r[1].BodyRefs
		switch args := sub.Arguments.(type) {
		case ledger.SetupStartupArguments:
			if len(refs) != 1 || refs[0] != args.SettingsSHA256 {
				return ErrInconsistent
			}
		case ledger.SetupChecksArguments:
			if int64(len(refs)) != args.BodyCount || bodySetDigest(refs) != args.BodySetSHA256 {
				return ErrInconsistent
			}
			for i, d := range refs {
				if d == (ledger.Digest{}) || i > 0 && bytes.Compare(refs[i-1][:], d[:]) >= 0 {
					return ErrInconsistent
				}
			}
		}
	}
	if sub.Operation == "colony.create" {
		if c.First != 1 || s.Managed {
			return ErrInconsistent
		}
		s.Managed = true
	} else if !s.Managed {
		return ErrInconsistent
	}
	switch args := sub.Arguments.(type) {
	case ledger.SetupWorkspaceArguments:
		w, exists := s.workspaces[sub.Target.ID]
		_, registered := s.workspaceRegistrations[sub.Target.ID]
		if !exists || !registered || w.Revision != receipt.Revision || w.CaptureInputsSHA256 != args.CaptureInputsSHA256 {
			return ErrInconsistent
		}
	case ledger.SetupContextArguments:
		if s.contexts[sub.Target.ID].Revision != sub.ExpectedRevision {
			return ErrInconsistent
		}
		// Existence and current policy limits are preflight-only. Forward
		// references and historic templates remain readable after limits change.
		s.contexts[sub.Target.ID] = Context{AgentID: sub.Target.AgentID, ContextID: sub.Target.ID, Revision: receipt.Revision, Settings: args}
	case ledger.SetupStartupArguments:
		if s.StartupRevision != sub.ExpectedRevision {
			return ErrInconsistent
		}
		s.Startup, s.StartupRevision = &args, receipt.Revision
	case ledger.SetupChecksArguments:
		if s.ChecksRevision != sub.ExpectedRevision {
			return ErrInconsistent
		}
		s.ChecksRevision = receipt.Revision
	}
	s.receipts[command.CommandID] = Receipt{CommandID: command.CommandID, Operation: sub.Operation, Target: Target{sub.Target.Kind, sub.Target.ID, sub.Target.AgentID}, SubmissionSHA256: digest, Revision: receipt.Revision, Sequence: c.Last}
	return nil
}

func bodySetDigest(refs []ledger.Digest) ledger.Digest {
	h := sha256.New()
	for _, digest := range refs {
		_, _ = io.WriteString(h, digest.String()+"\n")
	}
	var out ledger.Digest
	copy(out[:], h.Sum(nil))
	return out
}

func (s State) requireManaged() error {
	if s.Managed {
		return nil
	}
	if s.ThroughSequence == 0 {
		return fail("creation_incomplete", "", Target{Kind: "colony", ID: s.ColonyID}, nil)
	}
	return fail("legacy_configuration", "", Target{Kind: "colony", ID: s.ColonyID}, nil)
}

func (s State) contextChange(target Target, setting ledger.SetupContextArguments) error {
	n := s.nativeContexts[target.ID]
	if n.agent != target.AgentID {
		return fail("review_invalid", "context configure", target, nil)
	}
	if n.kind == "general" && (setting.DisplayTitle != "General work" || setting.TaskBinding != nil) || n.kind == "work" && setting.TaskBinding == nil || n.kind != "general" && n.kind != "work" {
		return fail("review_invalid", "context configure", target, nil)
	}
	old, configured := s.contexts[target.ID]
	if n.hasTurn {
		route := s.routes[setting.RouteID]
		if !configured || old.Settings.RouteID != setting.RouteID || old.Settings.CredentialScopeID != setting.CredentialScopeID || !reflect.DeepEqual(old.Settings.TaskBinding, setting.TaskBinding) || n.provider != route.Profile.Provider || n.model != route.Profile.ModelID {
			return fail("provider_move_requires_context_move", "context configure", target, nil)
		}
	}
	return nil
}
