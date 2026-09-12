package debrepo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Constraint is a single constraint inside a dependency relation, for example "libc6 (>= 2.34)" or
// "python3:any".
type Constraint struct {
	// Name is the package name (it can also be a virtual package name such as
	// "mail-transport-agent").
	Name string `json:"name"`
	// Arch is the architecture qualifier of the dependency, one of "any", "native", or a specific
	// architecture (such as "amd64"). It comes from a spelling like "python3:any"; empty means no
	// qualifier.
	Arch string `json:"arch,omitempty"`
	// Operator is the version relation: =, >=, <=, >>, <<, >, or <; empty means no version
	// restriction.
	Operator string `json:"operator,omitempty"`
	// Version is the version constraint; it only appears when Operator is non-empty.
	Version string `json:"version,omitempty"`
	// Archs is the architecture restriction list ([amd64 !i386]), kept as written (including the "!"
	// prefix).
	Archs []string `json:"archs,omitempty"`
	// Profiles is the build profile restriction (<!nocheck>), kept as written.
	Profiles []string `json:"profiles,omitempty"`
}

// IsVersioned reports whether the constraint carries a version restriction.
func (c Constraint) IsVersioned() bool { return c.Operator != "" }

// String returns a human-readable constraint description, for example "libc6 (>= 2.34)".
func (c Constraint) String() string {
	var sb strings.Builder
	sb.WriteString(c.Name)
	if c.Arch != "" {
		sb.WriteByte(':')
		sb.WriteString(c.Arch)
	}
	if c.IsVersioned() {
		sb.WriteString(" (")
		sb.WriteString(c.Operator)
		sb.WriteByte(' ')
		sb.WriteString(c.Version)
		sb.WriteByte(')')
	}
	if len(c.Archs) > 0 {
		sb.WriteString(" [")
		sb.WriteString(strings.Join(c.Archs, " "))
		sb.WriteByte(']')
	}
	if len(c.Profiles) > 0 {
		sb.WriteString(" <")
		sb.WriteString(strings.Join(c.Profiles, " "))
		sb.WriteByte('>')
	}
	return sb.String()
}

// Dependency is a single dependency relation, which may contain several alternatives separated by
// "|", for example "libc6 (>= 2.34) | libc6.1".
type Dependency struct {
	// Alternatives are the alternatives (at least one).
	Alternatives []Constraint `json:"alternatives"`
}

// Names returns every (alternative) package name involved in the dependency.
func (d Dependency) Names() []string {
	names := make([]string, 0, len(d.Alternatives))
	for _, alt := range d.Alternatives {
		names = append(names, alt.Name)
	}
	return names
}

// Matches reports whether the given package name appears in the dependency relation (version
// constraints are not compared).
func (d Dependency) Matches(name string) bool {
	for _, alt := range d.Alternatives {
		if alt.Name == name {
			return true
		}
	}
	return false
}

// String returns the textual form of the dependency relation, for example
// "libc6 (>= 2.34) | libc6.1".
func (d Dependency) String() string {
	parts := make([]string, 0, len(d.Alternatives))
	for _, alt := range d.Alternatives {
		parts = append(parts, alt.String())
	}
	return strings.Join(parts, " | ")
}

// MarshalJSON renders the dependency as a string.
func (d Dependency) MarshalJSON() ([]byte, error) { return marshalNoEscape(d.String()) }

// UnmarshalJSON parses a dependency relation in string form.
func (d *Dependency) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	parsed, err := ParseDependency(raw)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// Dependencies is the complete content of a dependency field (such as Depends): several dependencies
// separated by ",".
type Dependencies []Dependency

// Has reports whether the dependency list contains the given package name (version constraints and
// alternatives are ignored).
func (d Dependencies) Has(name string) bool {
	for _, dep := range d {
		if dep.Matches(name) {
			return true
		}
	}
	return false
}

// Find returns the first dependency relation that involves the given package name.
func (d Dependencies) Find(name string) (Dependency, bool) {
	for _, dep := range d {
		if dep.Matches(name) {
			return dep, true
		}
	}
	return Dependency{}, false
}

// Names returns every package name that appears in the dependency list (including alternatives, in
// order of appearance).
func (d Dependencies) Names() []string {
	var names []string
	for _, dep := range d {
		names = append(names, dep.Names()...)
	}
	return names
}

// String returns the textual form of the dependency list, for example "libc6 (>= 2.34), libssl3".
func (d Dependencies) String() string {
	parts := make([]string, 0, len(d))
	for _, dep := range d {
		parts = append(parts, dep.String())
	}
	return strings.Join(parts, ", ")
}

// MarshalJSON renders the list as an array of strings, one per dependency relation.
func (d Dependencies) MarshalJSON() ([]byte, error) {
	if d == nil {
		return []byte("null"), nil
	}
	items := make([]string, 0, len(d))
	for _, dep := range d {
		items = append(items, dep.String())
	}
	return marshalNoEscape(items)
}

// marshalNoEscape does not escape "<", ">", or "&" during serialization, which keeps a ">=" inside a
// dependency from being written as "\u003e=" (an outer encoder can still decide to escape them).
func marshalNoEscape(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}

// UnmarshalJSON parses either an array of strings or a whole block of dependency text.
func (d *Dependencies) UnmarshalJSON(data []byte) error {
	var items []string
	if err := json.Unmarshal(data, &items); err == nil {
		parsed := make(Dependencies, 0, len(items))
		for _, item := range items {
			dep, err := ParseDependency(item)
			if err != nil {
				return err
			}
			parsed = append(parsed, dep)
		}
		*d = parsed
		return nil
	}
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	parsed, err := ParseDependencies(raw)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// ParseDependencies parses a complete dependency field (such as
// "libc6 (>= 2.34), libssl3 | libssl1.1"). An empty field returns nil.
func ParseDependencies(field string) (Dependencies, error) {
	field = strings.TrimSpace(field)
	if field == "" {
		return nil, nil
	}
	// Multi-line fields (such as Uploaders and Build-Depends) are joined with "\n"; merge them into a
	// single line first.
	field = strings.ReplaceAll(field, "\n", " ")
	var deps Dependencies
	for _, part := range splitTopLevel(field, ',') {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		dep, err := ParseDependency(part)
		if err != nil {
			return nil, err
		}
		deps = append(deps, dep)
	}
	return deps, nil
}

// ParseDependency parses a single dependency relation ("|" can separate several alternatives).
func ParseDependency(raw string) (Dependency, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Dependency{}, fmt.Errorf("%w: empty dependency relation", ErrInvalidRelation)
	}
	var dep Dependency
	for _, part := range splitTopLevel(raw, '|') {
		alt, err := parseConstraint(part)
		if err != nil {
			return Dependency{}, err
		}
		dep.Alternatives = append(dep.Alternatives, alt)
	}
	if len(dep.Alternatives) == 0 {
		return Dependency{}, fmt.Errorf("%w: %q", ErrInvalidRelation, raw)
	}
	return dep, nil
}

// parseConstraint parses a single constraint: "name[:arch] [(op version)] [[archs]] [<profiles>]".
func parseConstraint(raw string) (Constraint, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Constraint{}, fmt.Errorf("%w: empty dependency item", ErrInvalidRelation)
	}
	var c Constraint
	rest := raw

	// Extract the version constraint first, so that its "<" and ">" are not confused with the
	// architecture and profile restrictions.
	if open := strings.IndexByte(rest, '('); open >= 0 {
		closeIdx := strings.IndexByte(rest[open:], ')')
		if closeIdx < 0 {
			return Constraint{}, fmt.Errorf("%w: the version constraint in %q is missing ')'", ErrInvalidRelation, raw)
		}
		versionPart := strings.TrimSpace(rest[open+1 : open+closeIdx])
		operator, version, err := parseVersionRelation(versionPart)
		if err != nil {
			return Constraint{}, fmt.Errorf("%w: %q: %w", ErrInvalidRelation, raw, err)
		}
		c.Operator, c.Version = operator, version
		rest = rest[:open] + rest[open+closeIdx+1:]
	}

	// Architecture restriction [amd64 !i386]
	for {
		open := strings.IndexByte(rest, '[')
		if open < 0 {
			break
		}
		closeIdx := strings.IndexByte(rest[open:], ']')
		if closeIdx < 0 {
			return Constraint{}, fmt.Errorf("%w: the architecture restriction in %q is missing ']'", ErrInvalidRelation, raw)
		}
		c.Archs = append(c.Archs, strings.Fields(rest[open+1:open+closeIdx])...)
		rest = rest[:open] + rest[open+closeIdx+1:]
	}

	// Build profile restriction <!nocheck>
	for {
		open := strings.IndexByte(rest, '<')
		if open < 0 {
			break
		}
		closeIdx := strings.IndexByte(rest[open:], '>')
		if closeIdx < 0 {
			return Constraint{}, fmt.Errorf("%w: the profile restriction in %q is missing '>'", ErrInvalidRelation, raw)
		}
		c.Profiles = append(c.Profiles, strings.Fields(rest[open+1:open+closeIdx])...)
		rest = rest[:open] + rest[open+closeIdx+1:]
	}

	name := strings.TrimSpace(rest)
	if name == "" {
		return Constraint{}, fmt.Errorf("%w: %q is missing a package name", ErrInvalidRelation, raw)
	}
	if colon := strings.IndexByte(name, ':'); colon >= 0 {
		c.Arch = strings.TrimSpace(name[colon+1:])
		name = strings.TrimSpace(name[:colon])
		if c.Arch == "" {
			return Constraint{}, fmt.Errorf("%w: the architecture qualifier of %q is empty", ErrInvalidRelation, raw)
		}
	}
	if name == "" {
		return Constraint{}, fmt.Errorf("%w: %q is missing a package name", ErrInvalidRelation, raw)
	}
	c.Name = name
	if !validPackageName(name) {
		return Constraint{}, fmt.Errorf("%w: invalid package name %q in %q", ErrInvalidRelation, name, raw)
	}
	return c, nil
}

// validPackageName reports whether the name follows the Debian package name rules: it consists of
// letters, digits, '+', '-', and '.', and starts with a letter or a digit.
func validPackageName(name string) bool {
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case isAlnum(c):
			continue
		case c == '+' || c == '-' || c == '.':
			if i == 0 {
				return false
			}
			continue
		default:
			return false
		}
	}
	return name != ""
}

// parseVersionRelation parses the ">= 1.0" inside "(>= 1.0)".
func parseVersionRelation(raw string) (operator, version string, err error) {
	raw = strings.TrimSpace(raw)
	for _, op := range []string{"<<", "<=", ">=", ">>", "=", "<", ">"} {
		if strings.HasPrefix(raw, op) {
			version = strings.TrimSpace(raw[len(op):])
			if version == "" {
				return "", "", fmt.Errorf("version constraint %q is missing a version number", raw)
			}
			return op, version, nil
		}
	}
	return "", "", fmt.Errorf("version constraint %q is missing a relation operator (=, >=, <=, >>, <<)", raw)
}

// splitTopLevel splits on a separator while ignoring separators inside parentheses and square
// brackets.
//
// Note that "<" and ">" must not be treated as nesting characters here, because a version constraint
// is written as "(<< 1.0)" or "(>= 2.0)" and its "<" and ">" would be mistaken for the start of a
// profile restriction.
func splitTopLevel(s string, sep byte) []string {
	var parts []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '[':
			depth++
		case ')', ']':
			if depth > 0 {
				depth--
			}
		default:
			if s[i] == sep && depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])
	return parts
}
