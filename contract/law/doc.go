// Package law loads and identifies the shared field law and the local D47
// validation policy independently of field execution. Version 1 is a draft
// until the owner's freeze (D40). This documentation is the format.
//
// # Field-law version 1
//
// Each artifact is one strict UTF-8 JSON object, without a BOM. Every member
// below is required; no other member is allowed and no omitted field receives
// a default. Object member order is free. Integer tokens contain unsigned
// decimal digits only: no sign, fraction or exponent. Leading zeros are JSON
// syntax errors. Strings and keys are decoded before validation: escaped
// spellings such as "\u0061ffordance" are accepted with a different byte identity.
// An escaped lone surrogate ("\ud800") is valid UTF-8, decodes to U+FFFD and
// reaches pattern, unknown_value or unknown_field, never an acceptance hole.
//
// The tables below give this version's validation order, not JSON member order.
// Root members:
//
//	format             string   dwarf-harness/undercurrent-law
//	schema_version     integer  1
//	evaluator_version  integer  1
//	kernel             string   bounded_best_arrival/v1
//	law_id             string   ^[a-z0-9][a-z0-9._-]{0,63}$
//	law_version        integer  1..2147483647
//	weight_scale_ppm   integer  exactly 1000000
//	kinds              array    exactly six kind objects, ascending by id
//
// law_id and law_version are labels, never identity. Kind objects:
//
//	id                string   one of the six registered kinds below
//	quantity_class    string   extensive or evidential, for any kind
//	unit              string   the kind's registered unit
//	sources           array    1..8 source objects, ascending by rule_id
//	hop_latency_ms    integer  0..86400000
//	attenuation_ppm   integer  1..999999
//	cutoff_units      integer  1..1000000000000
//	saturation_units  integer  1..1000000000000, strictly above cutoff_units
//
// Each kind must have at least one source, but may omit any other registered
// rule. No amplitude field exists here; producer magnitudes, reducers and
// saturation capping belong to the engine, not this loader. Source objects:
//
//	rule_id            string   a rule registered for the enclosing kind
//	mode               string   the rule's impulse, level, sustained or rising
//	unit               string   the enclosing kind's unit
//	half_life_seconds  integer  impulse: 1..31536000; other modes: exactly 0
//	age_rule_id        string   rising: linear_per_day/v1; otherwise: none
//	rise_units_per_day integer  rising: 1..1000000000000; otherwise: exactly 0
//	coupling           object   the rule's registered coupling profile
//
// Coupling objects:
//
//	profile     string  one of the four profiles below, as fixed by the rule
//	direction   string  local, dependency_to_consumer or resource_to_consumer
//	edge_types  array   0..2 edge-type strings, ascending and equal to the profile
//
// All kinds, sources and edge_types are strictly ascending by stable ASCII ID,
// with no duplicates. Unknown values and valid-but-mismatched registry values
// reject the entire law. The six kind/unit pairs, in ascending kind order:
//
//	affordance             affordance_units
//	dependency_strain      strain_units
//	epistemic_uncertainty  uncertainty_units
//	perturbation           perturbation_units
//	resource_pressure     pressure_units
//	surprise               surprise_units
//
// # Evaluator-v1 registry
//
// Rule IDs select built-in typed reducers, not event names, scripts or imports.
// In the rule table, L = local/v1, D = dependency_downstream/v1,
// O = overlap_relevance/v1 and R = registered_resource_outward/v1.
//
//	kind                   rule                                 mode       profile
//	affordance             repair_r2_impulse/v1                  impulse    D
//	affordance             repair_r3_impulse/v1                  impulse    D
//	affordance             repair_r3_sustained/v1                sustained  D
//	dependency_strain      validation_f2_impulse/v1              impulse    L
//	dependency_strain      validation_f2_sustained/v1            sustained  L
//	dependency_strain      validation_f3_attributed/v1           sustained  D
//	epistemic_uncertainty  base_staleness/v1                     rising     L
//	epistemic_uncertainty  check_flakiness/v1                    level      L
//	epistemic_uncertainty  check_quarantine/v1                   level      L
//	epistemic_uncertainty  check_retirement/v1                   level      L
//	epistemic_uncertainty  private_overlap/v1                    level      L
//	epistemic_uncertainty  promotion_overlap/v1                  impulse    O
//	perturbation           canonical_change/v1                   impulse    D
//	perturbation           federated_perturbation/v1              impulse    D
//	perturbation           provider_status/v1                    level      D
//	resource_pressure     core_budget_exposure/v1               level      R
//	resource_pressure     core_resource_usage/v1                level      R
//	resource_pressure     core_runner_queue/v1                  level      R
//	resource_pressure     router_headroom/v1                    level      R
//	surprise               check_duration_anomaly/v1             impulse    L
//	surprise               check_outcome_anomaly/v1              impulse    L
//	surprise               shared_resource_pressure_anomaly/v1   impulse    L
//
// Profiles fix both direction and the entire edge list:
//
//	local/v1                       local                   []
//	dependency_downstream/v1       dependency_to_consumer  [declared_dataflow, static_dependency]
//	overlap_relevance/v1           dependency_to_consumer  [declared_dataflow, static_dependency]
//	registered_resource_outward/v1 resource_to_consumer    [registered_resource_use]
//
// The only edge types are declared_dataflow, registered_resource_use and
// static_dependency. Encounter relations are not edges here. There is no
// validation_f1_unconfirmed/v1 rule. R1's router-headroom/v1 scheme (hyphen) is
// distinct from the registered router_headroom/v1 extractor (underscore).
//
// # Validation-policy version 1
//
// A separate local artifact contains exactly these root members, in check order:
//
//	format                     string   dwarf-harness/validation-policy
//	schema_version             integer  1
//	flake_rate_prior           rational
//	failure_repair_confidence  rational
//	quarantine_threshold       rational
//
// A rational object requires numerator followed by denominator in check order.
// Each is an integer in 1..18446744073709551615 (uint64), and numerator must be
// strictly below denominator. Fractions are not reduced: 1/20 and 2/40 are
// different artifacts. There is no label, policy ID or fourth policy parameter.
// These values never enter the shared field law.
//
// # Parser limits
//
// The host passes explicit LoadLimits; neither artifact supplies them. Every
// bound is inclusive, positive and at most the corresponding StandardV1 value.
// CompactV1 and StandardV1 return fresh values; a caller may tighten any field,
// never widen it. A zero LoadLimits or zero field is invalid, not a default.
// Both loaders use the same counting rules and profiles:
//
//	bound        compact-v1  standard-v1
//	Bytes        65536       262144
//	Depth        12          16
//	Members      64          128
//	Elements     128         256
//	StringBytes  128         256
//	Tokens       2048        8192
//
// Bytes counts the whole input. The root container is depth 1; each nested
// object or array adds one, and scalars add none. Members counts name/value
// pairs per object (a duplicate counts too), Elements counts values per array,
// and StringBytes counts decoded UTF-8 bytes per string, keys included.
// Tokens counts every value json.Decoder.Token returns, including keys and
// opening/closing delimiters; commas, colons and whitespace do not count.
// Valid v1 laws need at most depth 7, 8 members, 6 elements, 64-byte decoded
// strings and 681 tokens. Whitespace and escaped spellings can still inflate
// their raw byte size beyond compact-v1. A three-parameter policy needs depth
// 2, at most 5 members, 31-byte decoded strings and 27 tokens.
//
// # Refusal phases and errors
//
// Rejection is atomic: each loader returns its zero result and exactly one
// error, never a partial artifact. The loaders are pure, with no file, clock,
// environment or goroutine access. Successful results alias neither their
// input nor other results. The phases always run in this order:
//
//  0. Validate host limits, before touching input (ErrInvalidLimits).
//  1. Check strict JSON and resource bounds over the entire artifact.
//  2. Check the root type and head members before interpreting version 1.
//  3. Decode and validate the closed version-1 objects and arrays.
//
// Phase 1 first checks Bytes, then the BOM prefix, then UTF-8 over the entire
// input, then walks tokens in input order. At each token, the checks are syntax,
// Tokens, opened-container Depth, object Members, array Elements, StringBytes,
// duplicate decoded keys, then unsigned-integer spelling. After the root,
// anything but JSON whitespace (space, tab, LF, CR) is trailing_data, including
// another JSON value or a syntax error on a stray byte. Empty input is syntax.
// No json.Valid precheck or floating-point conversion is used. Phase-1 rules
// are invariants of the format family, kept by every schema and evaluator
// version; versions never relax these rules or reorder the phases.
//
// Phase 2 requires an object, then checks format, schema_version,
// evaluator_version and kernel for laws, or format and schema_version for
// policies. For each head member: absence is missing_field, a wrong JSON type
// is wrong_type, and another value is UnsupportedError. Unknown root members
// therefore cannot mask an unsupported version. Unsupported integer digits are
// preserved verbatim, including values too large for any Go integer.
//
// Phase 3 walks depth first in the documented table order. For each object it
// checks unknown members first (input order), missing members next (table
// order), then each member's type followed by its value rule (table order),
// then cross-member rules: saturation above cutoff, numerator below denominator,
// and an edge list equal to the registered profile. Null is always wrong_type.
// Each array checks count, then every element, then ascending order. Equal
// neighbours are duplicate; a decrease is unsorted, located at the later
// element's id, rule_id or edge index. Saturation cross-checks point to
// saturation_units, fraction cross-checks to numerator, and edge mismatches to
// the whole edge_types array. Kind units are checked before reading sources.
//
// Errors wrap exactly one sentinel via Unwrap; use errors.Is and errors.As:
//
//	ErrInvalidLimits  invalid caller bounds, phase 0
//	ErrLimit          *LimitError: Bound, Max, input Offset
//	ErrMalformed      *MalformedError: Problem, input Offset
//	ErrUnsupported    *UnsupportedError: Field, Value, Supported
//	ErrInvalid        *FieldError: Path, Violation
//
// Bound is bytes, depth, members, elements, string_bytes or tokens. Problem is
// bom, invalid_utf8, syntax, duplicate_key, not_unsigned_integer or trailing_data.
// FieldError paths are RFC 6901 JSON Pointers (empty for the root, ~0 for ~,
// ~1 for /). The closed violation codes are:
//
//	unknown_field  member not allowed at this position
//	missing_field  required member absent
//	wrong_type     wrong JSON type, including null
//	unknown_value  string outside this position's closed vocabulary
//	out_of_range   integer outside its domain, or failed numeric cross-check
//	pattern        law_id outside its ASCII pattern
//	count          array length outside its bounds
//	unsorted       array ID below its predecessor
//	duplicate      array ID equal to its predecessor
//	mismatch       known value not allowed by its rule, kind, mode or profile
//
// Error types, codes, paths and phase precedence are the contract. The order
// among several violations within a phase follows this version's documented
// table order and is pinned by tests, but is not a compatibility promise.
// Consumers must not branch on which of several violations is reported.
// Error() wording and exact diagnostic offsets are not compatibility promises;
// a BOM offset is 0 and a bytes-limit offset is Max.
//
// # Identity, local binding and exclusions
//
// SHA256 hashes the content bytes exactly as supplied, not git hash-object's
// object ID and not a Git "blob <size>\x00" header. It normalizes nothing: not
// whitespace, line endings, key order, escapes or rational terms. Only accepted
// artifacts get an identity. Digest.String returns 64 lowercase hexadecimal
// digits, the same text form as ledger.Digest without importing that package.
// The caller supplies immutable blob bytes, not a converted checkout. The
// repository's LF checkout rule does not change the loader's byte contract.
//
// Law.SHA256 supplies Core's field_law_sha256; ValidationPolicy.SHA256 supplies
// validation_policy_hash. A consumer reads the policy only as the bytes Core
// retains and binds by validation_policy_hash, never from a working-tree
// settings file: it is not a second settings file. The consumer checks
// policy.SHA256 == the manifest's validation_policy_hash (and law.SHA256 == its
// field_law_sha256 when consuming the law), then uses the typed parameters.
// Core retains approved bytes before referencing them in its activation record.
//
// Core alone supplies activation_id, epoch, effective_sequence, sorted local
// workspace/check/environment bindings, owner authorization, activation-request
// and expected-manifest hashes, and not_before_utc_ns. Neither artifact may
// carry these, grants, credentials or its own hash. The shared law additionally
// refuses D47 values and validation_policy_hash. All are unknown_field, whether
// at the root or nested. Effective sequence/time comes only from the actual
// Core activation record, never the artifact or a planned instant.
//
// # Compatibility and non-goals
//
// This version reads law schema 1, evaluator 1, bounded_best_arrival/v1, and
// policy schema 1, with the respective format strings above. Another head
// value is explicitly unsupported, not reinterpreted. Version 1 is closed and
// never gains optional members; additions require a new schema or evaluator
// alongside it. A new kind, rule or kernel requires a new evaluator version.
// Later contract versions must preserve the exact typed values and digests of
// these version-1 golden vectors and the evaluator semantics they identify.
// No upgrader converts an old artifact to new semantics. Goldens include
// alternate whitespace/key-order/CRLF identities and unsupported newer heads;
// their numeric candidates are test data, not the owner's chosen law.
//
// Loading does not execute a reducer, activate, approve, schedule, authorize,
// retain or retrieve anything. There is no encoder, field engine or local
// activation API. C4 can consume the policy without parsing the field law or
// starting U1.E. A loaded artifact is not engine readiness or activation
// approval. Consumers must wait for the owner's law-format freeze.
package law
