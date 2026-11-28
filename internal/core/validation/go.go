package validation

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// CanaryTemplate returns the exact public testing-error-v1 definition body.
// Substitution is host-owned; templates supplied by a caller are never executed.
func CanaryTemplate() []byte { return []byte(canaryTemplate) }

const canaryTemplate = `package %s

import (
	"os"
	"testing"
)

func TestDwarfCanaryV1(t *testing.T) {
	t.Logf("DWARF_CANARY_V1:%%s:%%d", %s, os.Getpid())
	t.Errorf("dwarf sentinel expected failure")
}
`

type canaryBinding struct {
	Protocol       string        `json:"protocol"`
	AttemptID      uuid.UUID     `json:"attempt_id"`
	RunID          uuid.UUID     `json:"run_id"`
	BindingSHA256  ledger.Digest `json:"binding_sha256"`
	TemplateSHA256 ledger.Digest `json:"template_sha256"`
	Nonce          string        `json:"nonce"`
}

func makeCanary(q ledger.ValidationAttemptQueued, c ledger.CheckSpec) ([]byte, canaryBinding, ledger.Digest, error) {
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, canaryBinding{}, ledger.Digest{}, err
	}
	binding := canaryBinding{c.Canary.Protocol, q.AttemptID, q.RunID, q.BindingSHA256, c.Canary.TemplateSHA256, hex.EncodeToString(nonce[:])}
	preimage, err := json.Marshal(binding)
	if err != nil {
		return nil, binding, ledger.Digest{}, err
	}
	return fmt.Appendf(nil, canaryTemplate, c.Canary.PackageName, strconv.Quote(string(preimage))), binding, sha256.Sum256(preimage), nil
}

// AdapterDigest binds a host executable to this adapter and its exact template.
func AdapterDigest(runner ledger.Digest) ledger.Digest {
	b, _ := json.Marshal(struct {
		Schema         int           `json:"schema"`
		RunnerSHA256   ledger.Digest `json:"runner_sha256"`
		Adapter        string        `json:"adapter"`
		AdapterVersion int           `json:"adapter_version"`
		TemplateSHA256 ledger.Digest `json:"template_sha256"`
	}{1, runner, "go-test-json", 1, sha256.Sum256(CanaryTemplate())})
	return sha256.Sum256(b)
}

func goArguments(c ledger.CheckSpec) ([]string, error) {
	if c.WallMS > int64((1<<63-1)/time.Millisecond) {
		return nil, ledger.ErrUnsupportedValidation
	}
	pkg := c.Inventory[0].Package
	var names []string
	for _, node := range c.Inventory {
		if node.Package != pkg {
			return nil, ledger.ErrUnsupportedValidation
		}
		top, _, _ := strings.Cut(node.Name, "/")
		if node.Parent == "" && node.Name != top {
			return nil, ledger.ErrUnsupportedValidation
		}
		if node.Parent != "" {
			at := strings.LastIndexByte(node.Name, '/')
			if at < 0 || node.Parent != node.Name[:at] {
				return nil, ledger.ErrUnsupportedValidation
			}
		}
		names = append(names, top)
	}
	names = append(names, c.Canary.TestName)
	slices.Sort(names)
	names = slices.Compact(names)
	for i := range names {
		names[i] = regexp.QuoteMeta(names[i])
	}
	return []string{"test", "-json", "-count=1", "-p=1", "-vet=off", "-timeout=" + (time.Duration(c.WallMS) * time.Millisecond).String(), "-run=^(" + strings.Join(names, "|") + ")$", c.Package}, nil
}
