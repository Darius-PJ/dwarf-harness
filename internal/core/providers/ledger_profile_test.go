package providers

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// This is deliberately on the provider side: contract imports never point inward.
func TestProfileLedgerDigestAgreement(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..", "..", "contract", "ledger", "testdata", "f3", "13")
	r, err := ledger.OpenReader(filepath.Join(root, "log"), ledger.ReadOptions{Colony: uuid.MustParse("019a1300-0000-7000-8000-000000000001"), Through: ledger.ToEnd, Consume: ledger.Consumption{ledger.TypeRuntimePaidRouteConfigured: {1}, ledger.TypeRuntimePaidReserved: {1}, ledger.TypeRuntimePaidReceipt: {1}}})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	profiles, reservations := 0, 0
	for {
		commit, err := r.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range commit.Records {
			switch record.Type {
			case ledger.TypeRuntimePaidRouteConfigured:
				p, err := ledger.DecodeRuntimePaidRouteConfigured(record.Payload)
				if err != nil {
					t.Fatal(err)
				}
				wire, err := p.Profile.Encode()
				if err != nil {
					t.Fatal(err)
				}
				provider, err := ParseProfile(wire)
				if err != nil {
					t.Fatal(err)
				}
				canonical, err := provider.Canonical()
				if err != nil || !bytes.Equal(canonical, wire) {
					t.Fatalf("profile encodings disagree: %v", err)
				}
				if !strings.Contains(provider.Sources[0].Quote, "&") || !strings.Contains(provider.Sources[0].Quote, "<") {
					t.Fatal("golden omitted escaping-sensitive source quote")
				}
				providerHash, err := provider.SHA256()
				if err != nil {
					t.Fatal(err)
				}
				ledgerHash, err := p.Profile.SHA256()
				if err != nil || providerHash != [32]byte(ledgerHash) {
					t.Fatal("profile digests disagree")
				}
				providerTariff, err := provider.TariffSHA256()
				if err != nil {
					t.Fatal(err)
				}
				ledgerTariff, err := p.Profile.TariffSHA256()
				if err != nil || providerTariff != [32]byte(ledgerTariff) {
					t.Fatal("tariff digests disagree")
				}
				last := commit.Records[len(commit.Records)-1]
				receipt, err := ledger.DecodeRuntimePaidReceipt(last.Payload)
				if err != nil {
					t.Fatal(err)
				}
				if receipt.Submission.Arguments.(ledger.PaidRouteArguments).ProfileSHA256 != ledgerHash {
					t.Fatal("golden receipt pins a different profile")
				}
				profiles++
			case ledger.TypeRuntimePaidReserved:
				p, err := ledger.DecodeRuntimePaidReserved(record.Payload)
				if err != nil {
					t.Fatal(err)
				}
				d := Descriptor{Provider: p.Request.Provider, Authority: p.Request.Authority, Model: p.Request.Model, Region: p.Request.Region, Method: p.Request.Method, Path: p.Request.Path, RequestedTier: p.Request.RequestedTier, CredentialScope: p.Request.Credential.ScopeID.String(), CredentialVersion: p.Request.Credential.Version.String()}
				for _, h := range p.Request.Headers {
					d.Headers = append(d.Headers, Header{h.Name, h.Value})
				}
				body, err := os.ReadFile(filepath.Join(root, "objects", "agents", "Ada-a1", "contexts", record.ContextID.String(), "core", "objects", p.InputObject.SHA256.String()))
				if err != nil {
					t.Fatal(err)
				}
				if sha256.Sum256(requestPreimage(d, body)) != [32]byte(p.Quote.RequestSHA256) {
					t.Fatal("golden request framing disagrees")
				}
				var components []Component
				for _, c := range p.Quote.Components {
					components = append(components, Component{c.Classes, c.MaxUnits, c.RateNumerator, c.RateDenominator})
				}
				hash, err := QuoteSHA256([32]byte(p.Quote.RequestSHA256), p.Quote.CapabilityVersion, p.Quote.TariffVersion, [32]byte(p.Quote.TariffSHA256), p.Quote.MaximumMicros, components)
				if err != nil || hash != [32]byte(p.Quote.QuoteSHA256) {
					t.Fatalf("golden quote framing disagrees: %v", err)
				}
				reservations++
			}
		}
	}
	if profiles != 3 || reservations != 6 {
		t.Fatalf("agreement coverage profiles=%d reservations=%d", profiles, reservations)
	}
}

func TestProfileDigestEscapesLikeJSONMarshal(t *testing.T) {
	t.Parallel()
	p := fixtureProfile()
	p.Sources[0].Quote = "Synthetic <>&\u2028\u2029 facts"
	wire, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(wire, []byte(`\u003c\u003e\u0026\u2028\u2029`)) {
		t.Fatal("profile escaped bytes differ from ledger")
	}
	known, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := p.SHA256()
	if err != nil || hash != sha256.Sum256(known) {
		t.Fatal("profile digest is not known-field JSON")
	}
	q, err := ledger.DecodePaidProfile(wire)
	if err != nil {
		t.Fatal(err)
	}
	ledgerHash, err := q.SHA256()
	if err != nil || hash != [32]byte(ledgerHash) {
		t.Fatal("escaped digest agreement failed")
	}
}
