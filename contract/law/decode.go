package law

import (
	"slices"
	"strconv"
	"strings"
)

func pointer(path, member string) string {
	return path + "/" + strings.ReplaceAll(strings.ReplaceAll(member, "~", "~0"), "/", "~1")
}

func checkHead(root node, policy bool) error {
	if root.kind != '{' {
		return &FieldError{"", "wrong_type"}
	}
	head := []struct {
		name      string
		kind      byte
		supported string
	}{
		{"format", 's', LawFormat}, {"schema_version", 'n', "1"},
	}
	if policy {
		head[0].supported = PolicyFormat
	} else {
		head = append(head, struct {
			name      string
			kind      byte
			supported string
		}{"evaluator_version", 'n', "1"}, struct {
			name      string
			kind      byte
			supported string
		}{"kernel", 's', string(BoundedBestArrivalV1)})
	}
	for _, field := range head {
		n, ok := root.get(field.name)
		if !ok {
			return &FieldError{pointer("", field.name), "missing_field"}
		}
		if n.kind != field.kind {
			return &FieldError{pointer("", field.name), "wrong_type"}
		}
		if n.text != field.supported {
			return &UnsupportedError{field.name, n.text, field.supported}
		}
	}
	return nil
}

// A decoder retains only the first field violation. Helpers become inert after
// it, allowing table-order decoding without returning any partial public value.
type decoder struct{ err error }

func (d *decoder) fail(path string, violation Violation) {
	if d.err == nil {
		d.err = &FieldError{path, violation}
	}
}
func (d *decoder) object(n node, path string, fields ...string) map[string]node {
	if d.err != nil {
		return nil
	}
	if n.kind != '{' {
		d.fail(path, "wrong_type")
		return nil
	}
	result := make(map[string]node, len(fields))
	for _, m := range n.members {
		if !slices.Contains(fields, m.name) {
			d.fail(pointer(path, m.name), "unknown_field")
			return nil
		}
		result[m.name] = m.value
	}
	for _, field := range fields {
		if _, ok := result[field]; !ok {
			d.fail(pointer(path, field), "missing_field")
			return nil
		}
	}
	return result
}
func (d *decoder) text(n node, path string) string {
	if d.err != nil {
		return ""
	}
	if n.kind != 's' {
		d.fail(path, "wrong_type")
		return ""
	}
	return n.text
}
func enum[T ~string](d *decoder, n node, path string, values ...T) T {
	value := T(d.text(n, path))
	if d.err == nil && !slices.Contains(values, value) {
		d.fail(path, "unknown_value")
	}
	return value
}
func (d *decoder) natural(n node, path string, min, max uint64) uint64 {
	if d.err != nil {
		return 0
	}
	if n.kind != 'n' {
		d.fail(path, "wrong_type")
		return 0
	}
	var value uint64
	for i := range n.text {
		digit := uint64(n.text[i] - '0') // phase 1 established unsigned digits
		if value > max/10 || value == max/10 && digit > max%10 {
			d.fail(path, "out_of_range")
			return 0
		}
		value = value*10 + digit
	}
	if value < min {
		d.fail(path, "out_of_range")
	}
	return value
}
func (d *decoder) array(n node, path string, min, max int) []node {
	if d.err != nil {
		return nil
	}
	if n.kind != '[' {
		d.fail(path, "wrong_type")
		return nil
	}
	if len(n.elements) < min || len(n.elements) > max {
		d.fail(path, "count")
		return nil
	}
	return n.elements
}
func (d *decoder) ascending(previous, current, path string) {
	if previous == current {
		d.fail(path, "duplicate")
	} else if previous > current {
		d.fail(path, "unsorted")
	}
}
func (d *decoder) unit(n node, path string, expected Unit) Unit {
	value := enum(d, n, path, AffordanceUnits, StrainUnits, UncertaintyUnits, PerturbationUnits, PressureUnits, SurpriseUnits)
	if value != expected {
		d.fail(path, "mismatch")
	}
	return value
}

func (d *decoder) law(n node) Law {
	m := d.object(n, "", "format", "schema_version", "evaluator_version", "kernel", "law_id", "law_version", "weight_scale_ppm", "kinds")
	result := Law{SchemaVersion: 1, EvaluatorVersion: 1, Kernel: BoundedBestArrivalV1}
	result.LawID = d.text(m["law_id"], "/law_id")
	if d.err == nil && !validLawID(result.LawID) {
		d.fail("/law_id", "pattern")
	}
	result.LawVersion = uint32(d.natural(m["law_version"], "/law_version", 1, 2147483647))
	result.WeightScalePPM = uint32(d.natural(m["weight_scale_ppm"], "/weight_scale_ppm", 1000000, 1000000))
	kinds := d.array(m["kinds"], "/kinds", 6, 6)
	result.Kinds = make([]KindLaw, len(kinds))
	for i, kind := range kinds {
		result.Kinds[i] = d.kind(kind, "/kinds/"+strconv.Itoa(i))
	}
	for i := 1; i < len(result.Kinds); i++ {
		d.ascending(string(result.Kinds[i-1].ID), string(result.Kinds[i].ID), "/kinds/"+strconv.Itoa(i)+"/id")
	}
	// Exactly six entries, the closed six-value enum and strict ascending order
	// imply completeness without a separate, potentially conflicting check.
	return result
}

func validLawID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for i := range id {
		c := id[i]
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			continue
		}
		if i > 0 && (c == '.' || c == '_' || c == '-') {
			continue
		}
		return false
	}
	return true
}

func (d *decoder) kind(n node, path string) KindLaw {
	m := d.object(n, path, "id", "quantity_class", "unit", "sources", "hop_latency_ms", "attenuation_ppm", "cutoff_units", "saturation_units")
	var result KindLaw
	result.ID = enum(d, m["id"], path+"/id", Affordance, DependencyStrain, EpistemicUncertainty, Perturbation, ResourcePressure, Surprise)
	result.QuantityClass = enum(d, m["quantity_class"], path+"/quantity_class", Extensive, Evidential)
	result.Unit = d.unit(m["unit"], path+"/unit", kindUnit(result.ID))
	sources := d.array(m["sources"], path+"/sources", 1, 8)
	result.Sources = make([]SourceLaw, len(sources))
	for i, source := range sources {
		result.Sources[i] = d.source(source, path+"/sources/"+strconv.Itoa(i), result.ID, result.Unit)
	}
	for i := 1; i < len(result.Sources); i++ {
		d.ascending(string(result.Sources[i-1].RuleID), string(result.Sources[i].RuleID), path+"/sources/"+strconv.Itoa(i)+"/rule_id")
	}
	result.HopLatencyMS = int64(d.natural(m["hop_latency_ms"], path+"/hop_latency_ms", 0, 86400000))
	result.AttenuationPPM = uint32(d.natural(m["attenuation_ppm"], path+"/attenuation_ppm", 1, 999999))
	result.CutoffUnits = d.natural(m["cutoff_units"], path+"/cutoff_units", 1, 1000000000000)
	result.SaturationUnits = d.natural(m["saturation_units"], path+"/saturation_units", 1, 1000000000000)
	if result.SaturationUnits <= result.CutoffUnits {
		d.fail(path+"/saturation_units", "out_of_range")
	}
	return result
}

func (d *decoder) source(n node, path string, kind Kind, unit Unit) SourceLaw {
	m := d.object(n, path, "rule_id", "mode", "unit", "half_life_seconds", "age_rule_id", "rise_units_per_day", "coupling")
	var result SourceLaw
	result.RuleID = RuleID(d.text(m["rule_id"], path+"/rule_id"))
	var rule sourceRule
	for _, r := range rulesV1() {
		if r.id == result.RuleID {
			rule = r
			break
		}
	}
	if rule.id == "" {
		d.fail(path+"/rule_id", "unknown_value")
	} else if rule.kind != kind {
		d.fail(path+"/rule_id", "mismatch")
	}
	result.Mode = enum(d, m["mode"], path+"/mode", Impulse, Level, Sustained, Rising)
	if result.Mode != rule.mode {
		d.fail(path+"/mode", "mismatch")
	}
	result.Unit = d.unit(m["unit"], path+"/unit", unit)
	minHalf, maxHalf := uint64(0), uint64(0)
	if result.Mode == Impulse {
		minHalf, maxHalf = 1, 31536000
	}
	result.HalfLifeSeconds = d.natural(m["half_life_seconds"], path+"/half_life_seconds", minHalf, maxHalf)
	result.AgeRuleID = enum(d, m["age_rule_id"], path+"/age_rule_id", AgeRuleNone, LinearPerDayV1)
	age, minRise, maxRise := AgeRuleNone, uint64(0), uint64(0)
	if result.Mode == Rising {
		age, minRise, maxRise = LinearPerDayV1, 1, 1000000000000
	}
	if result.AgeRuleID != age {
		d.fail(path+"/age_rule_id", "mismatch")
	}
	result.RiseUnitsPerDay = d.natural(m["rise_units_per_day"], path+"/rise_units_per_day", minRise, maxRise)
	result.Coupling = d.coupling(m["coupling"], path+"/coupling", rule.profile)
	return result
}

func (d *decoder) coupling(n node, path string, profile Profile) Coupling {
	m := d.object(n, path, "profile", "direction", "edge_types")
	var result Coupling
	result.Profile = enum(d, m["profile"], path+"/profile", LocalV1, DependencyDownstreamV1, OverlapRelevanceV1, RegisteredResourceOutwardV1)
	if result.Profile != profile {
		d.fail(path+"/profile", "mismatch")
	}
	expected := profileCoupling(profile)
	result.Direction = enum(d, m["direction"], path+"/direction", DirectionLocal, DependencyToConsumer, ResourceToConsumer)
	if result.Direction != expected.Direction {
		d.fail(path+"/direction", "mismatch")
	}
	edges := d.array(m["edge_types"], path+"/edge_types", 0, 2)
	result.EdgeTypes = make([]EdgeType, len(edges))
	for i, edge := range edges {
		result.EdgeTypes[i] = enum(d, edge, path+"/edge_types/"+strconv.Itoa(i), DeclaredDataflow, RegisteredResourceUse, StaticDependency)
	}
	for i := 1; i < len(result.EdgeTypes); i++ {
		d.ascending(string(result.EdgeTypes[i-1]), string(result.EdgeTypes[i]), path+"/edge_types/"+strconv.Itoa(i))
	}
	if !slices.Equal(result.EdgeTypes, expected.EdgeTypes) {
		d.fail(path+"/edge_types", "mismatch")
	}
	return result
}
