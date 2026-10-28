package ledger

import (
	"encoding/json"
	"errors"
	"math"
	"uuid"
)

// RegistrationSubmission is schema 5's canonical owner request, proposed for
// freeze (D125). Arguments is the matching concrete value, never arbitrary JSON.
// The operation and its arguments bind the kind-scoped target without a wrapper.
type RegistrationSubmission struct {
	Schema    int64  `json:"schema"`
	Actor     string `json:"actor"`
	Origin    Origin `json:"origin"`
	Operation string `json:"operation"`
	Arguments any    `json:"arguments"`
}

// ResourceRegistrationArguments binds an explicit, nonsecret external address.
// ResourceKey is never inferred from ResourceID; see RuntimeResourceRegistered.
type ResourceRegistrationArguments struct {
	ResourceID      uuid.UUID   `json:"resource_id"`
	ExpectedVersion int64       `json:"expected_version"`
	Resource        Resource    `json:"resource"`
	Audience        []uuid.UUID `json:"audience"`
}

type WorkDomainRegistrationArguments struct {
	WorkDomainID    uuid.UUID   `json:"work_domain_id"`
	ExpectedVersion int64       `json:"expected_version"`
	Audience        []uuid.UUID `json:"audience"`
}

type DeclarationArguments struct {
	DeclarationID   uuid.UUID             `json:"declaration_id"`
	ExpectedVersion int64                 `json:"expected_version"`
	Subject         RegistrationRef       `json:"subject"`
	Audience        []uuid.UUID           `json:"audience"`
	Consumers       []DeclarationConsumer `json:"consumers"`
}

func (p RegistrationSubmission) expectedVersion() int64 {
	switch a := p.Arguments.(type) {
	case ResourceRegistrationArguments:
		return a.ExpectedVersion
	case WorkDomainRegistrationArguments:
		return a.ExpectedVersion
	case DeclarationArguments:
		return a.ExpectedVersion
	default:
		return -1
	}
}

func (p RegistrationSubmission) check() error {
	if p.Schema != 5 || p.Actor != "owner" || !validOrigin(p.Origin) {
		return errors.New("invalid registration submission schema, actor or origin")
	}
	expected := p.expectedVersion()
	if expected < 0 || expected == math.MaxInt64 {
		return errors.New("invalid registration expected version")
	}
	valid := false
	switch a := p.Arguments.(type) {
	case ResourceRegistrationArguments:
		valid = p.Operation == "resource.set" && (RuntimeResourceRegistered{a.ResourceID, expected, expected + 1, a.Resource, a.Audience}).check() == nil
	case WorkDomainRegistrationArguments:
		valid = p.Operation == "work_domain.set" && (RuntimeWorkDomainRegistered{a.WorkDomainID, expected, expected + 1, a.Audience}).check() == nil
	case DeclarationArguments:
		valid = p.Operation == "declaration.set" && (RuntimeDeclarationChanged{a.DeclarationID, expected, expected + 1, a.Subject, a.Audience, a.Consumers}).check() == nil
	}
	if !valid {
		return errors.New("registration operation/argument mismatch or invalid arguments")
	}
	return nil
}

func (p RegistrationSubmission) Encode() (json.RawMessage, error) {
	return encodeRegistrationPayload("schema-5 submission", p, p.check())
}

func DecodeRegistrationSubmission(js json.RawMessage) (RegistrationSubmission, error) {
	var p RegistrationSubmission
	err := decodeRegistrationPayload("schema-5 submission", js, func(m *members) error {
		p = readRegistrationSubmission(m)
		_, err := p.Encode()
		return err
	})
	if err != nil {
		return RegistrationSubmission{}, err
	}
	return p, nil
}

func readRegistrationSubmission(m *members) RegistrationSubmission {
	p := RegistrationSubmission{Schema: m.nat("schema", true), Actor: m.text("actor"), Origin: graphObject(m, "origin", readOrigin), Operation: m.text("operation")}
	p.Arguments = graphObject(m, "arguments", func(b *members) any {
		switch p.Operation {
		case "resource.set":
			forbidScoped(b, "work_domain_id", "declaration_id", "subject", "consumers")
			a := ResourceRegistrationArguments{ResourceID: b.id("resource_id"), ExpectedVersion: b.nat("expected_version", true), Resource: graphObject(b, "resource", readResource)}
			a.Audience, _ = b.sortedIDs("audience")
			return a
		case "work_domain.set":
			forbidScoped(b, "resource_id", "resource", "declaration_id", "subject", "consumers")
			a := WorkDomainRegistrationArguments{WorkDomainID: b.id("work_domain_id"), ExpectedVersion: b.nat("expected_version", true)}
			a.Audience, _ = b.sortedIDs("audience")
			return a
		case "declaration.set":
			forbidScoped(b, "resource_id", "resource", "work_domain_id")
			a := DeclarationArguments{DeclarationID: b.id("declaration_id"), ExpectedVersion: b.nat("expected_version", true), Subject: graphObject(b, "subject", readRegistrationRef), Consumers: readScopedObjects(b, "consumers", readDeclarationConsumer)}
			a.Audience, _ = b.sortedIDs("audience")
			return a
		default:
			b.err = errors.New("unknown registration operation")
			return nil
		}
	})
	return p
}
