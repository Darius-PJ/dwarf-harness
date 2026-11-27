package ledger

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"unicode/utf8"
	"uuid"
)

// LauncherSubmissionSchema is reserved for F7-30, frozen (D188).
const LauncherSubmissionSchema = 11

// LauncherSubmission is a binding preimage, independent of any wire version.
type LauncherSubmission struct {
	Schema        int64                 `json:"schema"`
	Actor         string                `json:"actor"`
	Origin        Origin                `json:"origin"`
	Operation     string                `json:"operation"`
	RequestID     uuid.UUID             `json:"request_id"`
	RequestSHA256 Digest                `json:"request_sha256"`
	Arguments     LauncherBindArguments `json:"arguments"`
}

// LauncherBindArguments repeats the effect's members after request_id, in order.
type LauncherBindArguments struct {
	Principal         LauncherPrincipal `json:"principal"`
	Origin            Origin            `json:"origin"`
	Operation         string            `json:"operation"`
	TurnID            uuid.UUID         `json:"turn_id,omitzero"`
	TaskBinding       *TaskBinding      `json:"task_binding,omitempty"`
	Target            LauncherTarget    `json:"target"`
	RequestSHA256     Digest            `json:"request_sha256"`
	NativeAdmissionID uuid.UUID         `json:"native_admission_id,omitzero"`
}

func (p LauncherCommandBound) arguments() LauncherBindArguments {
	return LauncherBindArguments{p.Principal, p.Origin, p.Operation, p.TurnID, p.TaskBinding, p.Target, p.RequestSHA256, p.NativeAdmissionID}
}
func (a LauncherBindArguments) bound(id uuid.UUID) LauncherCommandBound {
	return LauncherCommandBound{id, a.Principal, a.Origin, a.Operation, a.TurnID, a.TaskBinding, a.Target, a.RequestSHA256, a.NativeAdmissionID}
}
func (p LauncherSubmission) check() error {
	if p.Schema != LauncherSubmissionSchema || p.Actor != "owner" || p.Operation != "launcher.bind" || p.Origin != p.Arguments.Origin || p.RequestSHA256 != p.Arguments.RequestSHA256 {
		return errors.New("invalid launcher submission")
	}
	return p.Arguments.bound(p.RequestID).check()
}
func (p LauncherSubmission) Encode() (json.RawMessage, error) {
	return encodeLauncherPayload("launcher submission", p, p.check())
}
func DecodeLauncherSubmission(js json.RawMessage) (LauncherSubmission, error) {
	var p LauncherSubmission
	err := decodeLauncherPayload("launcher submission", js, func(m *members) error { p = readLauncherSubmission(m); return p.check() })
	if err != nil {
		return LauncherSubmission{}, err
	}
	return p, nil
}
func readLauncherSubmission(m *members) LauncherSubmission {
	return LauncherSubmission{m.nat("schema", true), m.text("actor"), graphObject(m, "origin", readOrigin), m.text("operation"), m.id("request_id"), m.digest("request_sha256"), graphObject(m, "arguments", readLauncherBindArguments)}
}
func readLauncherBindArguments(m *members) LauncherBindArguments {
	a := LauncherBindArguments{Principal: graphObject(m, "principal", readLauncherPrincipal), Origin: graphObject(m, "origin", readOrigin), Operation: m.text("operation"), Target: graphObject(m, "target", readLauncherTarget), RequestSHA256: m.digest("request_sha256")}
	if a.Operation == "message.send" || a.Operation == "turn.cancel" {
		a.TurnID = m.id("turn_id")
	} else {
		forbidScoped(m, "turn_id")
	}
	if a.Operation == "message.send" {
		a.NativeAdmissionID = m.id("native_admission_id")
		a.TaskBinding = optionalObject(m, "task_binding", readTaskBinding)
	} else {
		forbidScoped(m, "native_admission_id", "task_binding")
	}
	return a
}

// LauncherMeaningVersion numbers only the frozen request-meaning preimage.
const LauncherMeaningVersion = 1

// LauncherMeaning excludes principals and all connection-local metadata.
// Arguments is exactly one of LauncherMessageSend, LauncherTurnCancel or
// LauncherPromotionRequest. Only omitted artifacts normalize, to an empty array.
type LauncherMeaning struct {
	Version     int64          `json:"version"`
	Origin      Origin         `json:"origin"`
	TaskBinding *TaskBinding   `json:"task_binding,omitempty"`
	CommandID   uuid.UUID      `json:"command_id"`
	Operation   string         `json:"operation"`
	Expected    LauncherTarget `json:"expected"`
	Arguments   any            `json:"arguments"`
}

type LauncherMessageSend struct {
	TurnID    uuid.UUID     `json:"turn_id"`
	Text      string        `json:"text"`
	Artifacts []ArtifactRef `json:"artifacts"`
}
type LauncherTurnCancel struct {
	TurnID uuid.UUID `json:"turn_id"`
}
type LauncherPromotionRequest struct {
	WorkspaceID uuid.UUID `json:"workspace_id"`
	Head        uuid.UUID `json:"head"`
	RestartFrom uuid.UUID `json:"restart_from,omitzero"`
}

func (p LauncherMeaning) Encode() (json.RawMessage, error) {
	valid := p.Version == LauncherMeaningVersion && validOrigin(p.Origin) && isV7(p.CommandID) && p.Expected.valid() && validTaskBinding(p.TaskBinding)
	switch a := p.Arguments.(type) {
	case LauncherMessageSend:
		valid = valid && p.Operation == "message.send" && isV7(a.TurnID) && utf8.ValidString(a.Text) && p.Expected.Kind == "context" && p.Expected.ID == p.Origin.ContextID
		for _, artifact := range a.Artifacts {
			valid = valid && validArtifact(artifact) && artifact.Bytes > 0
		}
		if a.Artifacts == nil {
			a.Artifacts = []ArtifactRef{}
		}
		p.Arguments = a
	case LauncherTurnCancel:
		valid = valid && p.Operation == "turn.cancel" && isV7(a.TurnID) && p.TaskBinding == nil && p.Expected.Kind == "continuation" && p.Expected.ID == a.TurnID
	case LauncherPromotionRequest:
		valid = valid && p.Operation == "promotion.request" && isV7(a.WorkspaceID) && isV7(a.Head) && (a.RestartFrom == (uuid.UUID{}) || isV7(a.RestartFrom)) && p.TaskBinding == nil && p.Expected.Kind == "context" && p.Expected.ID == p.Origin.ContextID
	default:
		valid = false
	}
	var err error
	if !valid {
		err = errors.New("invalid launcher request meaning")
	}
	return encodePayload("launcher meaning", p, err)
}

func (p LauncherMeaning) Digest() (Digest, error) {
	js, err := p.Encode()
	if err != nil {
		return Digest{}, err
	}
	return Digest(sha256.Sum256(js)), nil
}

func DecodeLauncherMeaning(js json.RawMessage) (LauncherMeaning, error) {
	var p LauncherMeaning
	err := decodePayload("launcher meaning", js, func(m *members) error {
		p = LauncherMeaning{Version: m.nat("version", true), Origin: graphObject(m, "origin", readOrigin), TaskBinding: optionalObject(m, "task_binding", readTaskBinding), CommandID: m.id("command_id"), Operation: m.text("operation"), Expected: graphObject(m, "expected", readLauncherTarget)}
		p.Arguments = graphObject(m, "arguments", func(a *members) any {
			switch p.Operation {
			case "message.send":
				forbidScoped(a, "workspace_id", "head", "restart_from")
				v := LauncherMessageSend{TurnID: a.id("turn_id"), Text: a.text("text"), Artifacts: []ArtifactRef{}}
				if _, exists := a.get("artifacts", false); exists {
					v.Artifacts = readScopedObjects(a, "artifacts", readArtifact)
				}
				return v
			case "turn.cancel":
				forbidScoped(a, "text", "artifacts", "workspace_id", "head", "restart_from")
				return LauncherTurnCancel{a.id("turn_id")}
			case "promotion.request":
				forbidScoped(a, "turn_id", "text", "artifacts")
				v := LauncherPromotionRequest{WorkspaceID: a.id("workspace_id"), Head: a.id("head")}
				v.RestartFrom, _ = a.optionalID("restart_from")
				return v
			default:
				a.err = errors.New("unknown launcher request meaning")
				return nil
			}
		})
		_, err := p.Encode()
		return err
	})
	if err != nil {
		return LauncherMeaning{}, err
	}
	return p, nil
}
