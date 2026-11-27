package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/agents"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
	"github.com/Darius-PJ/dwarf-harness/internal/core/setup"
	"github.com/Darius-PJ/dwarf-harness/internal/core/terminal"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

// runSetup is the positional command entrance. It never opens a session or
// enables the turn loop; all authority and idempotency remain in Core setup.
func runSetup(directory string, externalSettings bool, args []string, out io.Writer) int {
	operation := ""
	if len(args) >= 2 {
		operation = args[0] + " " + args[1]
	}
	operations := map[string]string{
		"colony create": "colony create", "workspace register": "workspace register", "workspace inputs": "workspace inputs",
		"agent create": "agent create", "context create": "context create", "context configure": "context configure",
		"task create": "task create", "grant issue": "grant issue", "capacity set": "capacity configure",
		"route set": "route set", "credential bind": "credential bind", "credential revoke": "credential revoke", "spend set": "spend set",
	}
	coreOperation, known := operations[operation]
	if !known {
		return writeSetupFailure(out, false, commandFailure("review_invalid", "command", "--input --review-out", setup.Target{}), 2)
	}
	flags := flag.NewFlagSet(operation, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	input := flags.String("input", "", "strict command input JSON")
	reviewOut := flags.String("review-out", "", "new private review file")
	apply := flags.String("apply", "", "approved review file")
	approve := flags.String("approve", "", "exact review SHA-256")
	selector := flags.String("context", "", "agent/context name or identity")
	agentSelector := flags.String("agent", "", "registered agent name or identity")
	workspaceSelector := flags.String("workspace", "", "registered workspace UUID")
	asJSON := flags.Bool("json", false, "safe structured result")
	if err := flags.Parse(args[2:]); err != nil || flags.NArg() != 0 || externalSettings {
		return writeSetupFailure(out, *asJSON, commandFailure("review_invalid", "command", operation+" --input --review-out", setup.Target{}), 2)
	}
	failure := func(err error) int {
		code := 1
		var f *setup.Failure
		if errors.As(err, &f) && (f.Code == "review_invalid" || f.Code == "review_changed" || f.Code == "approval_required") {
			code = 2
		}
		return writeSetupFailure(out, *asJSON, err, code)
	}
	applying := *apply != ""
	if applying && (*input != "" || *reviewOut != "" || *selector != "" || *agentSelector != "" || *workspaceSelector != "") || !applying && (*input == "" || *reviewOut == "" || *approve != "") {
		return failure(commandFailure("review_invalid", "command", operation+" --input --review-out", setup.Target{}))
	}
	var doc setup.ReviewDocument
	var approval ledger.Digest
	var raw []byte
	var err error
	if applying {
		if *approve == "" {
			return failure(commandFailure("approval_required", "approve", operation+" --apply --approve", setup.Target{}))
		}
		approval, err = ledger.ParseDigest(*approve)
		if err != nil {
			return failure(commandFailure("review_invalid", "approve", operation+" --apply --approve", setup.Target{}))
		}
		raw, err = readCommandFile(*apply, setup.MaxReviewBytes, "apply", operation)
		if err == nil {
			doc, err = setup.DecodeReview(raw)
		}
		if err != nil {
			return failure(err)
		}
		if doc.Operation != coreOperation || approval != doc.ReviewSHA256 {
			return failure(commandFailure("review_changed", "apply", operation+" --input --review-out", setup.Target{Kind: "colony", ID: doc.ColonyID}))
		}
	} else {
		raw, err = readCommandFile(*input, terminal.MaxSettingsBytes, "input", operation)
		if err != nil {
			return failure(err)
		}
	}
	ctx := context.Background()
	services := setup.Services{}
	closeServices := func() error { return nil }
	if operation != "colony create" {
		services, closeServices, err = openSetupServices(ctx, directory)
		if err != nil {
			return failure(err)
		}
	}
	if applying {
		var receipts []setup.Receipt
		receipts, err = setup.Apply(ctx, services, directory, doc, approval)
		err = errors.Join(err, closeServices())
		if err != nil {
			return failure(err)
		}
		if *asJSON {
			err = json.NewEncoder(out).Encode(struct {
				Status   string          `json:"status"`
				Receipts []setup.Receipt `json:"receipts"`
			}{"applied", receipts})
		} else {
			for _, receipt := range receipts {
				_, err = fmt.Fprintf(out, "applied command=%s operation=%s target=%s/%s revision=%d sequence=%d\n", receipt.CommandID, receipt.Operation, receipt.Target.Kind, receipt.Target.ID, receipt.Revision, receipt.Sequence)
				if err != nil {
					break
				}
			}
		}
	} else {
		raw, err = prepareSetupInput(ctx, services, coreOperation, raw, filepath.Dir(*input), *selector, *agentSelector, *workspaceSelector)
		if err == nil {
			doc, err = setup.Review(ctx, services, directory, coreOperation, raw)
		}
		err = errors.Join(err, closeServices())
		if err != nil {
			return failure(err)
		}
		if err = setup.WriteReview(*reviewOut, doc); err != nil {
			return failure(err)
		}
		if *asJSON {
			// Arguments, artifacts and source locators belong only to the explicit
			// human review, never the ordinary machine-readable summary.
			targets := make([]setup.Target, 0, len(doc.Steps))
			for _, step := range doc.Steps {
				targets = append(targets, step.Target)
			}
			err = json.NewEncoder(out).Encode(struct {
				Status       string         `json:"status"`
				ColonyID     uuid.UUID      `json:"colony_id"`
				ReviewSHA256 ledger.Digest  `json:"review_sha256"`
				Targets      []setup.Target `json:"targets"`
			}{"reviewed", doc.ColonyID, doc.ReviewSHA256, targets})
		} else {
			err = writeExactReview(out, doc)
		}
	}
	if err != nil {
		return 1
	}
	return 0
}

func openSetupServices(ctx context.Context, directory string) (setup.Services, func() error, error) {
	s := setup.Services{}
	closeAll := func() error {
		var err error
		if s.Agents != nil {
			err = errors.Join(err, s.Agents.Close())
		}
		if s.Workspace != nil {
			err = errors.Join(err, s.Workspace.Close())
		}
		if s.Store != nil {
			err = errors.Join(err, s.Store.Close())
		}
		return err
	}
	var err error
	s.Store, _, err = ledgerstore.Open(directory, ledger.Defined())
	if err != nil {
		return s, closeAll, startupFailure(err, setup.Target{Kind: "colony"}, "colony")
	}
	state, err := setup.Load(ctx, s.Store)
	if err == nil && !state.Managed {
		code, fix := "legacy_configuration", "--settings"
		if state.ThroughSequence == 0 {
			code, fix = "creation_incomplete", "colony create --apply --approve"
		}
		err = commandFailure(code, "colony", fix, setup.Target{Kind: "colony", ID: s.Store.Colony()})
	}
	if err == nil {
		s.Agents, _, err = agents.OpenPaid(ctx, s.Store, directory, agents.DefaultLimits(), agents.PaidOptions{Limits: agents.DefaultPaidLimits()})
	}
	if err == nil {
		profile := workspace.ProvisionalCapacityProfile()
		s.Workspace, _, err = workspace.Open(ctx, s.Store, directory, workspace.Config{Capacity: &profile})
	}
	if err != nil {
		return s, closeAll, errors.Join(err, closeAll())
	}
	return s, closeAll, nil
}

func readCommandFile(path string, limit int64, member, operation string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, commandFailure("review_invalid", member, operation+" --input --review-out", setup.Target{})
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	err = errors.Join(err, file.Close())
	if err != nil || int64(len(raw)) > limit {
		return nil, commandFailure("review_invalid", member, operation+" --input --review-out", setup.Target{})
	}
	return raw, nil
}

func prepareSetupInput(ctx context.Context, services setup.Services, operation string, raw []byte, base, selector, agentSelector, workspaceSelector string) ([]byte, error) {
	// RawMessage preserves the command-specific shape while the existing strict
	// parser rejects duplicate members, null, invalid UTF-8 and trailing input.
	// Core validates the adapted object against its concrete input type below.
	var strict struct {
		Input json.RawMessage `json:"input"`
	}
	wrapped := append(append([]byte(`{"input":`), raw...), '}')
	if err := terminal.DecodeObject(wrapped, terminal.MaxSettingsBytes+10, &strict); err != nil {
		return nil, commandFailure("review_invalid", "$", operation+" --input --review-out", setup.Target{})
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(strict.Input, &members); err != nil {
		return nil, commandFailure("review_invalid", "$", operation+" --input --review-out", setup.Target{})
	}
	invalid := func(member string) error {
		return commandFailure("review_invalid", member, operation+" --input --review-out", setup.Target{})
	}
	set := func(key string, value any) { members[key], _ = json.Marshal(value) }
	if workspaceSelector != "" {
		if operation != "workspace inputs" {
			return nil, invalid("workspace_id")
		}
		id, err := uuid.Parse(workspaceSelector)
		if err != nil {
			return nil, invalid("workspace_id")
		}
		if raw, exists := members["workspace_id"]; exists {
			var supplied uuid.UUID
			if json.Unmarshal(raw, &supplied) != nil || supplied != id {
				return nil, invalid("workspace_id")
			}
		}
		set("workspace_id", id)
	}
	if agentSelector != "" {
		if operation != "context create" {
			return nil, invalid("agent_id")
		}
		snapshot, err := services.Agents.OwnerSetupSnapshot(ctx)
		if err != nil {
			return nil, err
		}
		var id uuid.UUID
		for _, identity := range snapshot.Contexts {
			if identity.Directory == agentSelector || identity.AgentID.String() == agentSelector {
				if id != (uuid.UUID{}) && id != identity.AgentID {
					return nil, invalid("agent_id")
				}
				id = identity.AgentID
			}
		}
		if id == (uuid.UUID{}) {
			return nil, commandFailure("review_invalid", "agent_id", "agent create --input --review-out", setup.Target{Kind: "agent"})
		}
		if value, exists := members["agent_id"]; exists {
			var supplied uuid.UUID
			if json.Unmarshal(value, &supplied) != nil || supplied != id {
				return nil, invalid("agent_id")
			}
		}
		set("agent_id", id)
	}
	artifact := func(pathKey, dataKey string) error {
		pathRaw, exists := members[pathKey]
		if !exists {
			return invalid(pathKey)
		}
		if _, exists := members[dataKey]; exists {
			return invalid(dataKey)
		}
		var path string
		if json.Unmarshal(pathRaw, &path) != nil || path == "" {
			return invalid(pathKey)
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(base, path)
		}
		limit := agents.DefaultLimits().MaxObjectBytes
		if pathKey == "profile_file" {
			limit = int64(agents.DefaultPaidLimits().MaxProfileBytes)
		}
		data, err := readCommandFile(path, limit, pathKey, operation)
		if err != nil {
			return err
		}
		delete(members, pathKey)
		set(dataKey, data)
		return nil
	}
	mint := func(settings map[string]json.RawMessage, work bool) {
		if _, ok := settings["route_id"]; !ok {
			settings["route_id"], _ = json.Marshal(uuid.NewV7())
			settings["expected_route_version"] = json.RawMessage("1")
		}
		if _, ok := settings["credential_scope_id"]; !ok {
			settings["credential_scope_id"], _ = json.Marshal(uuid.NewV7())
		}
		if work {
			if _, ok := settings["task_binding"]; !ok {
				settings["task_binding"], _ = json.Marshal(ledger.TaskBinding{TaskID: uuid.NewV7(), AssignmentID: uuid.NewV7(), Generation: 1})
			}
		}
	}
	switch operation {
	case "agent create":
		if err := artifact("instructions_file", "instructions_base64"); err != nil {
			return nil, err
		}
		if _, exists := members["settings"]; exists {
			return nil, invalid("settings")
		}
		var general map[string]json.RawMessage
		if data, exists := members["general"]; !exists || json.Unmarshal(data, &general) != nil {
			return nil, invalid("general")
		}
		mint(general, false)
		delete(members, "general")
		set("settings", general)
	case "context create", "context configure":
		settings := make(map[string]json.RawMessage)
		for _, key := range []string{"task_binding", "route_id", "expected_route_version", "credential_scope_id", "max_output_tokens", "turn_policy"} {
			if value, ok := members[key]; ok {
				settings[key] = value
				delete(members, key)
			}
		}
		if value, ok := members["title"]; ok {
			settings["display_title"] = value
			delete(members, "title")
		}
		if _, exists := members["settings"]; exists {
			return nil, invalid("settings")
		}
		if operation == "context create" {
			mint(settings, true)
		}
		set("settings", settings)
	case "route set":
		if err := artifact("profile_file", "profile_base64"); err != nil {
			return nil, err
		}
	case "workspace register", "workspace inputs":
		var repository string
		if json.Unmarshal(members["repository"], &repository) != nil || repository == "" {
			return nil, invalid("repository")
		}
		if !filepath.IsAbs(repository) {
			repository = filepath.Join(base, repository)
		}
		set("repository", repository)
		if operation == "workspace inputs" {
			var id uuid.UUID
			if raw, exists := members["workspace_id"]; exists {
				if json.Unmarshal(raw, &id) != nil || id == (uuid.UUID{}) {
					return nil, invalid("workspace_id")
				}
			}
			id, err := resolveSetupWorkspace(ctx, services.Workspace, repository, id)
			if err != nil {
				return nil, err
			}
			set("workspace_id", id)
		}
	}
	if selector != "" {
		if operation != "route set" && operation != "credential bind" && operation != "task create" && operation != "context configure" {
			return nil, invalid("context")
		}
		if _, exists := members["context_id"]; exists {
			return nil, invalid("context_id")
		}
		binding, err := resolveSetupContext(ctx, services, selector)
		if err != nil {
			return nil, err
		}
		set("context_id", binding.ContextID)
	}
	return json.Marshal(members)
}

func resolveSetupWorkspace(ctx context.Context, service *workspace.Service, repository string, selected uuid.UUID) (uuid.UUID, error) {
	failure := commandFailure("review_invalid", "workspace_id", "workspace register --input --review-out", setup.Target{Kind: "workspace", ID: selected})
	registered, err := service.ListWorkspaces()
	if err != nil {
		return uuid.UUID{}, err
	}
	commonDir := ""
	if selected == (uuid.UUID{}) {
		proposal, err := workspace.Inspect(ctx, repository)
		if err != nil {
			return uuid.UUID{}, commandFailure("review_invalid", "repository", "workspace register --input --review-out", setup.Target{Kind: "workspace"})
		}
		commonDir = proposal.CommonDir
	}
	var result uuid.UUID
	for _, candidate := range registered {
		if selected != (uuid.UUID{}) && candidate.ID == selected || selected == (uuid.UUID{}) && candidate.CommonDir == commonDir {
			if result != (uuid.UUID{}) {
				return uuid.UUID{}, failure
			}
			result = candidate.ID
		}
	}
	if result == (uuid.UUID{}) {
		return result, failure
	}
	return result, nil
}

func resolveSetupContext(ctx context.Context, services setup.Services, selector string) (agents.Binding, error) {
	failure := commandFailure("review_invalid", "context", "context configure --input --review-out", setup.Target{Kind: "context"})
	agentName, contextName, ok := strings.Cut(selector, "/")
	if !ok || agentName == "" || contextName == "" {
		return agents.Binding{}, failure
	}
	state, err := setup.Load(ctx, services.Store)
	if err != nil {
		return agents.Binding{}, err
	}
	snapshot, err := services.Agents.OwnerSetupSnapshot(ctx)
	if err != nil {
		return agents.Binding{}, err
	}
	var result agents.Binding
	for _, c := range state.Contexts {
		for _, identity := range snapshot.Contexts {
			if identity.ContextID == c.ContextID && (identity.Directory == agentName || identity.AgentID.String() == agentName) && (c.Settings.DisplayTitle == contextName || c.ContextID.String() == contextName) {
				if result != (agents.Binding{}) {
					return agents.Binding{}, failure
				}
				result = identity.Binding
			}
		}
	}
	if result == (agents.Binding{}) {
		return result, failure
	}
	return result, nil
}

func writeExactReview(out io.Writer, doc setup.ReviewDocument) error {
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "Exact owner review (no operation applied):\n%s\n", raw); err != nil {
		return err
	}
	for _, artifact := range doc.Artifacts {
		if _, err := fmt.Fprintf(out, "artifact %s bytes=%d exact text=%q\n", artifact.SHA256, artifact.Bytes, artifact.Data); err != nil {
			return err
		}
	}
	for _, step := range doc.Steps {
		if step.Operation != "workspace.register" && step.Operation != "workspace.inputs" {
			continue
		}
		var arguments struct {
			Proposal workspace.Proposal `json:"proposal"`
		}
		if err := json.Unmarshal(step.Arguments, &arguments); err != nil {
			return err
		}
		p := arguments.Proposal
		for _, file := range []struct {
			name  string
			value workspace.HostFile
		}{{"local_excludes", p.LocalExcludes}, {"global_excludes", p.GlobalExcludes}, {"local_attributes", p.LocalAttributes}, {"global_attributes", p.GlobalAttributes}, {"system_attributes", p.SystemAttributes}} {
			if _, err := fmt.Fprintf(out, "%s exact text=%q\n", file.name, file.value.Bytes); err != nil {
				return err
			}
		}
	}
	_, err = fmt.Fprintf(out, "approve review_sha256=%s with --apply --approve\n", doc.ReviewSHA256)
	return err
}

func commandFailure(code, path, fix string, target setup.Target) *setup.Failure {
	return &setup.Failure{Code: code, Path: path, FixCommand: fix, Target: target}
}

func writeSetupFailure(out io.Writer, asJSON bool, err error, exit int) int {
	f := startupFailure(err, setup.Target{Kind: "colony"}, "$")
	if asJSON {
		_ = json.NewEncoder(out).Encode(f)
	} else {
		_, _ = fmt.Fprintln(out, describe(f))
	}
	return exit
}
