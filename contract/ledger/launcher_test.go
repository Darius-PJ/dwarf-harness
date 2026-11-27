package ledger

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"uuid"
)

func launcherFixture() LauncherCommandBound {
	uid := int64(1000)
	o := scopedOriginFixture()
	return LauncherCommandBound{
		RequestID: eligibilityID(0x7001), Principal: LauncherPrincipal{Kind: "unix_uid", UID: &uid},
		Origin: o, Operation: "message.send", TurnID: eligibilityID(0x7002),
		Target:        LauncherTarget{Kind: "context", ID: o.ContextID, Revision: 1},
		RequestSHA256: scopedHash("Synthetic request meaning."), NativeAdmissionID: eligibilityID(0x7003),
	}
}

func launcherSubmission(p LauncherCommandBound) LauncherSubmission {
	return LauncherSubmission{LauncherSubmissionSchema, "owner", p.Origin, "launcher.bind", p.RequestID, p.RequestSHA256, p.arguments()}
}

func TestLauncherBindRefusals(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name string
		edit func(*LauncherCommandBound)
	}{
		{"operation", func(p *LauncherCommandBound) { p.Operation = "host.shutdown" }},
		{"missing_turn", func(p *LauncherCommandBound) { p.TurnID = uuid.UUID{} }},
		{"missing_admission", func(p *LauncherCommandBound) { p.NativeAdmissionID = uuid.UUID{} }},
		{"unexpected_turn", func(p *LauncherCommandBound) { p.Operation = "promotion.request"; p.NativeAdmissionID = uuid.UUID{} }},
		{"unexpected_admission", func(p *LauncherCommandBound) {
			p.Operation = "turn.cancel"
			p.Target = LauncherTarget{"continuation", p.TurnID, 1}
		}},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			p := launcherFixture()
			row.edit(&p)
			if _, err := p.Encode(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("encoded invalid binding: %v", err)
			}
			raw, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeLauncherCommandBound(raw); !errors.Is(err, ErrInvalid) {
				t.Fatalf("decoded invalid binding: %v", err)
			}
		})
	}
	t.Run("actor", func(t *testing.T) {
		t.Parallel()
		s := launcherSubmission(launcherFixture())
		s.Actor = "core"
		raw, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeLauncherSubmission(raw); !errors.Is(err, ErrInvalid) {
			t.Fatalf("decoded non-owner: %v", err)
		}
	})
	t.Run("receipt_digest", func(t *testing.T) {
		t.Parallel()
		s := launcherSubmission(launcherFixture())
		r := LauncherCommandReceipt{eligibilityID(0x7004), Digest(sha256.Sum256(mustEncode(s))), s, "recorded", 2}
		r.SubmissionSHA256 = scopedHash("Another submission.")
		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeLauncherCommandReceipt(raw); !errors.Is(err, ErrInvalid) {
			t.Fatalf("decoded mismatched receipt: %v", err)
		}
	})
	t.Run("group_order", func(t *testing.T) {
		t.Parallel()
		g := mustBuild(t, launcherLog)
		commits, _, err := readAll(g.dir, ReadOptions{Colony: g.colony, Through: ToEnd, Consume: launcherTypes})
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range commits {
			if c.Records[len(c.Records)-1].Type != TypeLauncherCommandReceipt {
				continue
			}
			c.Records[0], c.Records[1] = c.Records[1], c.Records[0]
			if err := checkLauncherGoldenGroup(c, commits); err == nil {
				t.Fatal("accepted reordered group")
			}
			return
		}
		t.Fatal("fixture lacks launcher group")
	})
}

func TestLauncherMeaning(t *testing.T) {
	t.Parallel()
	p := launcherFixture()
	m := LauncherMeaning{1, p.Origin, nil, p.RequestID, p.Operation, p.Target, LauncherMessageSend{TurnID: p.TurnID, Text: "Synthetic owner input."}}
	a, err := m.Digest()
	if err != nil {
		t.Fatal(err)
	}
	args := m.Arguments.(LauncherMessageSend)
	args.Artifacts = []ArtifactRef{}
	m.Arguments = args
	b, err := m.Digest()
	if err != nil || a != b {
		t.Fatalf("omission changed meaning: %v", err)
	}
	args.Text += " Changed."
	m.Arguments = args
	c, err := m.Digest()
	if err != nil || c == a {
		t.Fatalf("text did not change meaning: %v", err)
	}
	args.Text = "Synthetic owner input."
	m.Arguments = args
	m.Expected.Revision++
	d, err := m.Digest()
	if err != nil || d == a {
		t.Fatalf("revision did not change meaning: %v", err)
	}
}
