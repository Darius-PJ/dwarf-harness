package agents

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

const fixtureSeedFuzzRoot = "DWARF_AGENTS_FUZZ_SEED_CONTAINER"

// Only closed trees and encoded values survive a build. No fixture owns a seed,
// and the seed root never enters the environment inherited by crash children.
var fixtureSeeds = struct {
	rootOnce   sync.Once
	root       string
	rootErr    error
	fuzzRoot   string
	fuzzWorker bool
	mu         sync.Mutex
	entries    map[string]*fixtureSeed
}{entries: make(map[string]*fixtureSeed)}

type fixtureSeed struct {
	once    sync.Once
	root    string
	failure string
	digest  [sha256.Size]byte
	paid    *paidFixtureSeed
}

type paidFixtureSeed struct {
	origin                      ledger.Origin
	route, credential, template json.RawMessage
	grant, inspect, turn        uuid.UUID
	next                        int
}

// The coordinator owns workers' seed trees because Go may kill a worker before
// its TestMain cleanup. Ordinary runs and crash children ignore this container.
func prepareFixtureSeeds() error {
	fixtureSeeds.fuzzWorker = flag.Lookup("test.fuzzworker").Value.String() == "true"
	if fixtureSeeds.fuzzWorker {
		fixtureSeeds.fuzzRoot = os.Getenv(fixtureSeedFuzzRoot)
		if fixtureSeeds.fuzzRoot == "" {
			return errors.New("fuzz worker missing fixture seed container")
		}
		return nil
	}
	if flag.Lookup("test.fuzz").Value.String() == "" {
		return nil
	}
	root, err := os.MkdirTemp(os.TempDir(), "dwarf-agents-fuzz-")
	if err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err == nil {
		err = os.Setenv(fixtureSeedFuzzRoot, resolved)
	}
	if err != nil {
		return errors.Join(err, os.RemoveAll(root))
	}
	fixtureSeeds.fuzzRoot = resolved
	return nil
}

// Fatal calls must retain their first cause: sync.Once also finishes on Goexit.
// The preinstalled failure covers an abandoned build that never calls Fatal.
type fixtureSeedTB struct {
	testing.TB
	seed   *fixtureSeed
	failed bool
}

func (t *fixtureSeedTB) Fatal(args ...any) {
	t.Helper()
	if !t.failed {
		t.seed.failure, t.failed = fmt.Sprint(args...), true
	}
	t.TB.Fatal(args...)
}

func (t *fixtureSeedTB) Fatalf(format string, args ...any) {
	t.Helper()
	if !t.failed {
		t.seed.failure, t.failed = fmt.Sprintf(format, args...), true
	}
	t.TB.Fatalf(format, args...)
}

func copyFixtureSeed(t testing.TB, destination, key string, build func(testing.TB, string) *paidFixtureSeed) *paidFixtureSeed {
	t.Helper()
	fixtureSeeds.mu.Lock()
	seed := fixtureSeeds.entries[key]
	if seed == nil {
		seed = &fixtureSeed{}
		fixtureSeeds.entries[key] = seed
	}
	fixtureSeeds.mu.Unlock()
	seed.once.Do(func() {
		seed.failure = "fixture seed build abandoned"
		builder := &fixtureSeedTB{TB: t, seed: seed}
		fixtureSeeds.rootOnce.Do(func() {
			parent := os.TempDir()
			if fixtureSeeds.fuzzWorker {
				parent = fixtureSeeds.fuzzRoot
			}
			fixtureSeeds.root, fixtureSeeds.rootErr = os.MkdirTemp(parent, "dwarf-agents-seeds-")
			if fixtureSeeds.rootErr == nil {
				var resolved string
				resolved, fixtureSeeds.rootErr = filepath.EvalSymlinks(fixtureSeeds.root)
				if fixtureSeeds.rootErr == nil {
					fixtureSeeds.root = resolved
				}
			}
		})
		if fixtureSeeds.rootErr != nil {
			builder.Fatal(fixtureSeeds.rootErr)
		}
		var err error
		seed.root, err = os.MkdirTemp(fixtureSeeds.root, "fixture-")
		if err != nil {
			builder.Fatal(err)
		}
		seed.paid = build(builder, seed.root)
		seed.digest, err = fixtureSeedDigest(seed.root)
		if err != nil {
			builder.Fatal(err)
		}
		seed.failure = ""
	})
	if seed.failure != "" {
		t.Fatalf("fixture seed %s: %s", key, seed.failure)
	}
	if err := os.CopyFS(destination, os.DirFS(seed.root)); err != nil {
		t.Fatal(err)
	}
	return seed.paid
}

func fixtureSeedDigest(root string) ([sha256.Size]byte, error) {
	h := sha256.New()
	err := fs.WalkDir(os.DirFS(root), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			fmt.Fprintf(h, "dir %q\n", name)
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("non-regular fixture seed entry %s", name)
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "file %q %d\n", name, len(data))
		_, _ = h.Write(data)
		return nil
	})
	var digest [sha256.Size]byte
	copy(digest[:], h.Sum(nil))
	return digest, err
}

// Called only after m.Run has joined all tests and their cleanup functions.
func finishFixtureSeeds() error {
	var result error
	for key, seed := range fixtureSeeds.entries {
		if seed.failure != "" {
			result = errors.Join(result, fmt.Errorf("fixture seed %s: %s", key, seed.failure))
			continue
		}
		digest, err := fixtureSeedDigest(seed.root)
		if err != nil {
			result = errors.Join(result, fmt.Errorf("fixture seed %s: %w", key, err))
		} else if digest != seed.digest {
			result = errors.Join(result, fmt.Errorf("fixture seed %s changed", key))
		}
	}
	if fixtureSeeds.root != "" {
		result = errors.Join(result, os.RemoveAll(fixtureSeeds.root))
	}
	// CoordinateFuzzing joins every worker before the coordinator's m.Run returns.
	if fixtureSeeds.fuzzRoot != "" && !fixtureSeeds.fuzzWorker {
		result = errors.Join(result, os.RemoveAll(fixtureSeeds.fuzzRoot))
	}
	return result
}

func closeSeedFixture(f *serviceFixture) error {
	var result error
	if f.service != nil {
		result = f.service.Close()
		f.service = nil
	}
	if f.store != nil {
		result = errors.Join(result, f.store.Close())
		f.store = nil
	}
	return result
}

func copyPaidFixture(t testing.TB, period string) *paidFixture {
	t.Helper()
	root := testRoot(t)
	seed := copyFixtureSeed(t, root, "paid-"+period, func(t testing.TB, root string) *paidFixtureSeed {
		return buildPaidSeed(t, root, period)
	})
	store, _, err := ledgerstore.Open(root, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	f := &paidFixture{serviceFixture: &serviceFixture{root: root, store: store}, origin: seed.origin, grant: seed.grant, inspect: seed.inspect, turn: seed.turn, next: seed.next}
	t.Cleanup(func() { _ = closeSeedFixture(f.serviceFixture) })
	f.route, err = ledger.DecodeRuntimePaidRouteConfigured(seed.route)
	if err != nil {
		t.Fatal(err)
	}
	f.credential, err = ledger.DecodeRuntimeCredentialChanged(seed.credential)
	if err != nil {
		t.Fatal(err)
	}
	f.template, err = ledger.DecodeRuntimePaidReserved(seed.template)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
