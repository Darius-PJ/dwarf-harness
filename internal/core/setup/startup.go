package setup

import (
	"context"
	"crypto/sha256"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// Configuration is the early-start selection. The host decodes HostSettings
// with its current host-settings decoder after this read, before opening Core
// services. No credential source is resolved here.
type Configuration struct {
	Managed        bool
	Contexts       []Context
	HostSettings   []byte
	LegacySettings []byte
}

// LoadConfiguration must run immediately after Store.Open. A nil external file
// means --settings was absent; an explicitly supplied empty file is still an
// external file and conflicts with a managed colony.
func LoadConfiguration(ctx context.Context, store setupReader, external []byte) (Configuration, error) {
	state, err := Load(ctx, store)
	if err != nil {
		return Configuration{}, classify(err, "start", Target{})
	}
	if !state.Managed {
		if state.ThroughSequence == 0 {
			return Configuration{}, fail("creation_incomplete", "start", Target{Kind: "colony", ID: state.ColonyID}, nil)
		}
		if external == nil {
			return Configuration{}, fail("settings_required", "start", Target{Kind: "colony", ID: state.ColonyID}, nil)
		}
		return Configuration{LegacySettings: append([]byte{}, external...)}, nil
	}
	if external != nil {
		return Configuration{}, fail("mixed_configuration", "start", Target{Kind: "colony", ID: state.ColonyID}, nil)
	}
	if len(state.Contexts) == 0 {
		return Configuration{}, fail("setup_required", "start", Target{Kind: "colony", ID: state.ColonyID}, nil)
	}
	out := Configuration{Managed: true, Contexts: state.Contexts}
	if state.Startup != nil {
		body, err := store.ReadBody(ctx, state.Startup.SettingsSHA256)
		if err != nil {
			return Configuration{}, classify(err, "start", Target{Kind: "colony", ID: state.ColonyID})
		}
		if int64(len(body)) != state.Startup.SettingsBytes || ledger.Digest(sha256.Sum256(body)) != state.Startup.SettingsSHA256 {
			return Configuration{}, ErrInconsistent
		}
		out.HostSettings = body
	}
	return out, nil
}
