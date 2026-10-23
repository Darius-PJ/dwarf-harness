package agents

import (
	"encoding/json"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func nonnegativeCapacity(c ledger.Capacity) bool {
	return c.TurnSlots >= 0 && c.LocalSlots >= 0 && c.BufferBytes >= 0 && c.LocalUnits >= 0 && c.MaxOperationBytes >= 0
}
func capacityWithin(c, parent ledger.Capacity) bool {
	return nonnegativeCapacity(c) && nonnegativeCapacity(parent) && c.TurnSlots <= parent.TurnSlots && c.LocalSlots <= parent.LocalSlots && c.BufferBytes <= parent.BufferBytes && c.LocalUnits <= parent.LocalUnits && c.MaxOperationBytes <= parent.MaxOperationBytes
}

type grantRules struct {
	id, parent uuid.UUID
	grantee    Binding
	operations []string
	resources  []ledger.Resource
	dataClass  string
	recipients []Binding
	limits     ledger.Capacity
	depth      int64
	lifetime   string
	task       ledger.TaskBinding
	revoked    bool
}

type authorityRequest struct {
	grant                uuid.UUID
	origin               Binding
	operation, dataClass string
	targets              []ledger.Resource
	recipients           []Binding
	task                 ledger.TaskBinding
	bytes                int64
}

func validTaskRestriction(task ledger.TaskBinding) bool {
	return task == (ledger.TaskBinding{}) || validID(task.TaskID) && validID(task.AssignmentID) && task.Generation > 0
}

func validGrant(g grantRules) bool {
	if !validID(g.id) || !validID(g.grantee.AgentID) || !validID(g.grantee.ContextID) ||
		g.parent != (uuid.UUID{}) && !validID(g.parent) || g.depth < 0 || !nonnegativeCapacity(g.limits) ||
		!validTaskRestriction(g.task) || g.lifetime != "until_revoked" || len(g.operations) == 0 ||
		!strictStrings(g.operations) || len(g.resources) == 0 || len(g.recipients) == 0 || !validRecipients(g.recipients) {
		return false
	}
	externalEffects := 0
	for _, operation := range g.operations {
		if operation == "grant.issue" || operation == "grant.revoke" {
			continue
		}
		if !classOperation(g.dataClass, operation) {
			return false
		}
		if g.dataClass == "external" {
			externalEffects++
		}
	}
	if externalEffects > 1 {
		return false
	}
	for i, resource := range g.resources {
		if !validResource(resource) || resourceClass(resource.Kind) != g.dataClass || i > 0 && compareResource(g.resources[i-1], resource) >= 0 {
			return false
		}
		if resource.Kind == "task" && resource.TaskID == (uuid.UUID{}) {
			for _, operation := range g.operations {
				if operation != "task.create" && operation != "grant.issue" && operation != "grant.revoke" {
					return false
				}
			}
		}
	}
	return true
}

func resourceClass(kind string) string {
	if kind == "context" {
		return "private"
	}
	return kind
}

func classOperation(class, op string) bool {
	switch class {
	case "private":
		return slices.Contains([]string{"context.inspect", "private.read", "private.write", "turn.start", "turn.inspect", "turn.cancel"}, op)
	case "task":
		return slices.Contains([]string{"task.inspect", "task.create", "task.claim", "task.assign", "task.start", "task.wait", "task.resume", "task.submit", "task.complete", "task.fail", "task.cancel", "task.reassign"}, op)
	case "workspace":
		return slices.Contains([]string{"file.read", "file.search", "file.write", "file.edit"}, op)
	case "artifact":
		return op == "artifact.read"
	case "external":
		return slices.Contains([]string{"shell.execute", "process.spawn", "network.connect", "connector.call", "model.call"}, op)
	default:
		return false
	}
}

func validResource(r ledger.Resource) bool {
	_, err := json.Marshal(r)
	return err == nil
}

func validRecipients(recipients []Binding) bool {
	for i, b := range recipients {
		if !validID(b.AgentID) || !validID(b.ContextID) || i > 0 && compareBinding(recipients[i-1], b) >= 0 {
			return false
		}
	}
	return true
}

func recipientSubset(parent, child []Binding) bool {
	if !validRecipients(parent) || !validRecipients(child) {
		return false
	}
	for _, recipient := range child {
		if _, ok := slices.BinarySearchFunc(parent, recipient, compareBinding); !ok {
			return false
		}
	}
	return true
}

func resourcesContain(parent, child []ledger.Resource) bool {
	for _, target := range child {
		if !slices.ContainsFunc(parent, func(region ledger.Resource) bool { return resourceContains(region, target) }) {
			return false
		}
	}
	return true
}

func grantAttenuates(parent, child grantRules) bool {
	return validGrant(parent) && validGrant(child) && !parent.revoked && !child.revoked && child.parent == parent.id &&
		slices.Contains(parent.operations, "grant.issue") && slices.Contains(parent.recipients, child.grantee) &&
		child.depth < parent.depth && child.dataClass == parent.dataClass && capacityWithin(child.limits, parent.limits) &&
		stringSubset(parent.operations, child.operations) && resourcesContain(parent.resources, child.resources) &&
		recipientSubset(parent.recipients, child.recipients) && (parent.task == (ledger.TaskBinding{}) || child.task == parent.task)
}

// chainAuthorizes takes exactly one selected leaf followed by its ancestors.
// It neither searches a store nor combines sibling grants into a permission.
// The caller supplies current task facts and repeats this check at its boundary.
func chainAuthorizes(chain []grantRules, request authorityRequest) bool {
	if len(chain) == 0 || chain[0].id != request.grant || chain[0].grantee != request.origin ||
		len(request.targets) == 0 || request.bytes < 0 || !validTaskRestriction(request.task) {
		return false
	}
	for _, target := range request.targets {
		if !validResource(target) {
			return false
		}
		if request.operation != "grant.issue" && request.operation != "grant.revoke" {
			if target.Kind == "workspace" || target.Kind == "context" && target.Scope == "context" {
				if target.Binding != ledger.Binding(request.origin) {
					return false
				}
			} else if target.Kind == "context" && target.Binding.AgentID != request.origin.AgentID {
				return false
			}
		}
	}
	seen := make(map[uuid.UUID]bool, len(chain))
	for i, grant := range chain {
		if seen[grant.id] || !validGrant(grant) || grant.revoked || grant.dataClass != request.dataClass ||
			!slices.Contains(grant.operations, request.operation) || !resourcesContain(grant.resources, request.targets) ||
			!recipientSubset(grant.recipients, request.recipients) || request.bytes > grant.limits.MaxOperationBytes ||
			grant.task != (ledger.TaskBinding{}) && grant.task != request.task {
			return false
		}
		seen[grant.id] = true
		if i+1 < len(chain) {
			if !grantAttenuates(chain[i+1], grant) {
				return false
			}
		} else if grant.parent != (uuid.UUID{}) {
			return false
		}
	}
	return true
}
