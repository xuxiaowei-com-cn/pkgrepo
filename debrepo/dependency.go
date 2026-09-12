package debrepo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Constraint 是依赖关系中的一个具体约束，例如 "libc6 (>= 2.34)"、"python3:any"。
type Constraint struct {
	// Name 是包名（也可能是虚拟包名，如 "mail-transport-agent"）。
	Name string `json:"name"`
	// Arch 是依赖的架构限定，取值 "any"、"native" 或具体架构（如 "amd64"），
	// 来自 "python3:any" 这样的写法，为空表示未限定。
	Arch string `json:"arch,omitempty"`
	// Operator 是版本关系：=、>=、<=、>>、<<、>、<，为空表示不限制版本。
	Operator string `json:"operator,omitempty"`
	// Version 是版本约束，仅在 Operator 非空时出现。
	Version string `json:"version,omitempty"`
	// Archs 是架构限制列表（[amd64 !i386]），保留原始写法（含 "!" 前缀）。
	Archs []string `json:"archs,omitempty"`
	// Profiles 是构建 profile 限制（<!nocheck>），保留原始写法。
	Profiles []string `json:"profiles,omitempty"`
}

// IsVersioned 判断约束是否带版本限制。
func (c Constraint) IsVersioned() bool { return c.Operator != "" }

// String 返回可读的约束描述，例如 "libc6 (>= 2.34)"。
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

// Dependency 是一条依赖关系，可能包含多个用 "|" 分隔的替代方案，
// 例如 "libc6 (>= 2.34) | libc6.1"。
type Dependency struct {
	// Alternatives 是替代方案（至少一个）。
	Alternatives []Constraint `json:"alternatives"`
}

// Names 返回该依赖涉及的所有（替代）包名。
func (d Dependency) Names() []string {
	names := make([]string, 0, len(d.Alternatives))
	for _, alt := range d.Alternatives {
		names = append(names, alt.Name)
	}
	return names
}

// Matches 判断依赖关系中是否出现指定包名（不比较版本约束）。
func (d Dependency) Matches(name string) bool {
	for _, alt := range d.Alternatives {
		if alt.Name == name {
			return true
		}
	}
	return false
}

// String 返回依赖关系的文本形式，例如 "libc6 (>= 2.34) | libc6.1"。
func (d Dependency) String() string {
	parts := make([]string, 0, len(d.Alternatives))
	for _, alt := range d.Alternatives {
		parts = append(parts, alt.String())
	}
	return strings.Join(parts, " | ")
}

// MarshalJSON 输出为字符串。
func (d Dependency) MarshalJSON() ([]byte, error) { return marshalNoEscape(d.String()) }

// UnmarshalJSON 解析字符串形式的依赖关系。
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

// Dependencies 是一个依赖字段（如 Depends）的完整内容：多条依赖之间用 "," 分隔。
type Dependencies []Dependency

// Has 判断依赖列表中是否包含指定包名（忽略版本约束与替代方案）。
func (d Dependencies) Has(name string) bool {
	for _, dep := range d {
		if dep.Matches(name) {
			return true
		}
	}
	return false
}

// Find 返回第一条涉及指定包名的依赖关系。
func (d Dependencies) Find(name string) (Dependency, bool) {
	for _, dep := range d {
		if dep.Matches(name) {
			return dep, true
		}
	}
	return Dependency{}, false
}

// Names 返回依赖列表中出现过的所有包名（含替代方案，按出现顺序）。
func (d Dependencies) Names() []string {
	var names []string
	for _, dep := range d {
		names = append(names, dep.Names()...)
	}
	return names
}

// String 返回依赖列表的文本形式，例如 "libc6 (>= 2.34), libssl3"。
func (d Dependencies) String() string {
	parts := make([]string, 0, len(d))
	for _, dep := range d {
		parts = append(parts, dep.String())
	}
	return strings.Join(parts, ", ")
}

// MarshalJSON 输出为字符串数组，每项是一条依赖关系。
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

// marshalNoEscape 序列化时不转义 "<" ">" "&"，
// 避免依赖里的 ">=" 被写成 "\u003e="（外层编码器仍可自行决定是否转义）。
func marshalNoEscape(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}

// UnmarshalJSON 解析字符串数组或整段依赖文本。
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

// ParseDependencies 解析一个完整的依赖字段（如 "libc6 (>= 2.34), libssl3 | libssl1.1"）。
// 空字段返回 nil。
func ParseDependencies(field string) (Dependencies, error) {
	field = strings.TrimSpace(field)
	if field == "" {
		return nil, nil
	}
	// 多行字段（如 Uploaders、Build-Depends）用 "\n" 连接，先合并为一行。
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

// ParseDependency 解析一条依赖关系（可用 "|" 分隔多个替代方案）。
func ParseDependency(raw string) (Dependency, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Dependency{}, fmt.Errorf("%w: 依赖关系为空", ErrInvalidRelation)
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

// parseConstraint 解析单个约束："name[:arch] [(op version)] [[archs]] [<profiles>]"。
func parseConstraint(raw string) (Constraint, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Constraint{}, fmt.Errorf("%w: 空的依赖项", ErrInvalidRelation)
	}
	var c Constraint
	rest := raw

	// 先摘出版本约束，避免其中的 "<" ">" 与架构/profile 限制混淆。
	if open := strings.IndexByte(rest, '('); open >= 0 {
		closeIdx := strings.IndexByte(rest[open:], ')')
		if closeIdx < 0 {
			return Constraint{}, fmt.Errorf("%w: %q 中的版本约束缺少 ')'", ErrInvalidRelation, raw)
		}
		versionPart := strings.TrimSpace(rest[open+1 : open+closeIdx])
		operator, version, err := parseVersionRelation(versionPart)
		if err != nil {
			return Constraint{}, fmt.Errorf("%w: %q: %w", ErrInvalidRelation, raw, err)
		}
		c.Operator, c.Version = operator, version
		rest = rest[:open] + rest[open+closeIdx+1:]
	}

	// 架构限制 [amd64 !i386]
	for {
		open := strings.IndexByte(rest, '[')
		if open < 0 {
			break
		}
		closeIdx := strings.IndexByte(rest[open:], ']')
		if closeIdx < 0 {
			return Constraint{}, fmt.Errorf("%w: %q 中的架构限制缺少 ']'", ErrInvalidRelation, raw)
		}
		c.Archs = append(c.Archs, strings.Fields(rest[open+1:open+closeIdx])...)
		rest = rest[:open] + rest[open+closeIdx+1:]
	}

	// 构建 profile 限制 <!nocheck>
	for {
		open := strings.IndexByte(rest, '<')
		if open < 0 {
			break
		}
		closeIdx := strings.IndexByte(rest[open:], '>')
		if closeIdx < 0 {
			return Constraint{}, fmt.Errorf("%w: %q 中的 profile 限制缺少 '>'", ErrInvalidRelation, raw)
		}
		c.Profiles = append(c.Profiles, strings.Fields(rest[open+1:open+closeIdx])...)
		rest = rest[:open] + rest[open+closeIdx+1:]
	}

	name := strings.TrimSpace(rest)
	if name == "" {
		return Constraint{}, fmt.Errorf("%w: %q 缺少包名", ErrInvalidRelation, raw)
	}
	if colon := strings.IndexByte(name, ':'); colon >= 0 {
		c.Arch = strings.TrimSpace(name[colon+1:])
		name = strings.TrimSpace(name[:colon])
		if c.Arch == "" {
			return Constraint{}, fmt.Errorf("%w: %q 的架构限定为空", ErrInvalidRelation, raw)
		}
	}
	if name == "" {
		return Constraint{}, fmt.Errorf("%w: %q 缺少包名", ErrInvalidRelation, raw)
	}
	c.Name = name
	if !validPackageName(name) {
		return Constraint{}, fmt.Errorf("%w: %q 中的包名 %q 非法", ErrInvalidRelation, raw, name)
	}
	return c, nil
}

// validPackageName 判断是否符合 Debian 的包名规则：
// 由字母、数字、'+'、'-'、'.' 组成，且以字母或数字开头。
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

// parseVersionRelation 解析 "(>= 1.0)" 中的 ">= 1.0"。
func parseVersionRelation(raw string) (operator, version string, err error) {
	raw = strings.TrimSpace(raw)
	for _, op := range []string{"<<", "<=", ">=", ">>", "=", "<", ">"} {
		if strings.HasPrefix(raw, op) {
			version = strings.TrimSpace(raw[len(op):])
			if version == "" {
				return "", "", fmt.Errorf("版本约束 %q 缺少版本号", raw)
			}
			return op, version, nil
		}
	}
	return "", "", fmt.Errorf("版本约束 %q 缺少关系运算符（=、>=、<=、>>、<<）", raw)
}

// splitTopLevel 按分隔符切分，但忽略括号与方括号内部的分隔符。
//
// 注意：这里不能把 "<" ">" 当作层级符号，因为版本约束里会写作
// "(<< 1.0)"、"(>= 2.0)"，其中的 "<" ">" 会被误判为 profile 限制的开始。
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
