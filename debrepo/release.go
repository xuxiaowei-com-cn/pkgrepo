package debrepo

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Time 是 deb822 中的时间戳，例如 Release 文件的
// "Date: Sat, 11 Jul 2026 09:02:23 UTC"。
type Time struct {
	// Raw 是原始字符串。
	Raw   string
	value time.Time
}

// timeLayouts 是 Release 文件中出现过的几种时间格式。
var timeLayouts = []string{
	time.RFC1123,
	time.RFC1123Z,
	time.RFC822,
	time.RFC822Z,
	time.RFC3339,
	"Mon, 2 Jan 2006 15:04:05 MST",
	"Mon, 2 Jan 2006 15:04:05 -0700",
}

// ParseTime 解析 Release 文件中的时间戳，空字符串返回零值。
func ParseTime(raw string) (Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Time{}, nil
	}
	for _, layout := range timeLayouts {
		if value, err := time.Parse(layout, raw); err == nil {
			return Time{Raw: raw, value: value}, nil
		}
	}
	return Time{}, fmt.Errorf("debrepo: 非法的时间戳 %q", raw)
}

// Time 返回 time.Time，为空时返回零时间。
func (t Time) Time() time.Time { return t.value }

// IsZero 判断时间戳是否为空。
func (t Time) IsZero() bool { return t.value.IsZero() }

// IsSet 判断时间戳是否已解析。
func (t Time) IsSet() bool { return !t.value.IsZero() }

// After 判断时间戳是否晚于 other（为空时返回 false）。
func (t Time) After(other time.Time) bool { return !t.value.IsZero() && t.value.After(other) }

// String 返回 RFC3339 格式（UTC）的时间，为空时返回空字符串。
func (t Time) String() string {
	if t.value.IsZero() {
		return ""
	}
	return t.value.UTC().Format(time.RFC3339)
}

// Format 按 layout 格式化时间，为空时返回空字符串。
func (t Time) Format(layout string) string {
	if t.value.IsZero() {
		return ""
	}
	return t.value.Format(layout)
}

// MarshalJSON 输出为 RFC3339 字符串，零值输出 null。
func (t Time) MarshalJSON() ([]byte, error) {
	if t.value.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(t.String())
}

// UnmarshalJSON 解析 RFC3339 字符串或 Release 风格的时间戳。
func (t *Time) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	parsed, err := ParseTime(raw)
	if err != nil {
		return err
	}
	*t = parsed
	return nil
}

// FileEntry 是 Release 校验值分节中的一行，描述索引文件的大小与摘要；
// Sources 索引也用同样的结构描述源码包中的各个文件。
type FileEntry struct {
	// Path 是相对仓库根（Release）或源码包目录（Sources）的路径。
	Path string `json:"path"`
	// Size 是文件大小（字节）。
	Size int64 `json:"size"`
	// Checksum 是文件的摘要。
	Checksum Checksum `json:"checksum"`
}

// Release 是 dists/<suite>/Release（或 InRelease）的解析结果：
// 仓库的发行信息与全部索引文件的校验值。
type Release struct {
	// Origin、Label 是仓库来源。
	Origin string `json:"origin,omitempty"`
	Label  string `json:"label,omitempty"`
	// Suite 是套件名（stable、testing），Codename 是代号（bookworm、trixie）。
	Suite    string `json:"suite,omitempty"`
	Codename string `json:"codename,omitempty"`
	// Version 是发行版版本号，例如 "13.6"。
	Version string `json:"version,omitempty"`
	// Description 是发行说明。
	Description string `json:"description,omitempty"`
	// Date 是生成时间，ValidUntil 是过期时间（可能为空）。
	Date       Time `json:"date,omitempty"`
	ValidUntil Time `json:"valid_until,omitempty"`
	// AcquireByHash 表示仓库支持 by-hash 方式获取索引（仓库更新时的原子性保障）。
	AcquireByHash bool `json:"acquire_by_hash,omitempty"`
	// NotAutomatic、ButAutomaticUpgrades 是第三方仓库的 apt 行为标记。
	NotAutomatic         bool `json:"not_automatic,omitempty"`
	ButAutomaticUpgrades bool `json:"but_automatic_upgrades,omitempty"`
	// Architectures、Components 是仓库提供的架构与组件列表。
	Architectures []string `json:"architectures,omitempty"`
	Components    []string `json:"components,omitempty"`

	stanza  *Stanza
	entries []FileEntry
	paths   []string
	byPath  map[string]FileEntry
	byAlgo  map[string]map[string]FileEntry
}

// checksumSections 是 Release 文件中的校验值分节名称。
var checksumSections = map[string]string{
	"md5sum": "md5",
	"sha1":   "sha1",
	"sha256": "sha256",
	"sha512": "sha512",
}

// ParseRelease 解析 Release 或 InRelease 的内容（InRelease 的 PGP 签名会被自动剥离）。
func ParseRelease(r io.Reader) (*Release, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("debrepo: 读取 Release 失败: %w", err)
	}
	cleared := ClearPGPArmor(data)
	var first *Stanza
	err = ParseStanzasFunc(strings.NewReader(string(cleared)), func(stanza *Stanza) error {
		if first == nil {
			copied := *stanza
			first = &copied
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if first == nil || first.Len() == 0 {
		return nil, fmt.Errorf("%w: Release 文件为空", ErrNotRepository)
	}
	return parseReleaseStanza(first)
}

func parseReleaseStanza(stanza *Stanza) (*Release, error) {
	release := &Release{
		stanza: stanza,
		byPath: make(map[string]FileEntry, 16),
		byAlgo: make(map[string]map[string]FileEntry, 4),
	}
	release.Origin = stanza.Get("Origin")
	release.Label = stanza.Get("Label")
	release.Suite = stanza.Get("Suite")
	release.Codename = stanza.Get("Codename")
	release.Version = stanza.Get("Version")
	release.Description = stanza.Get("Description")
	release.AcquireByHash = parseYesNo(stanza.Get("Acquire-By-Hash"))
	release.NotAutomatic = parseYesNo(stanza.Get("NotAutomatic")) || parseYesNo(stanza.Get("Not-Automatic"))
	release.ButAutomaticUpgrades = parseYesNo(stanza.Get("ButAutomaticUpgrades")) ||
		parseYesNo(stanza.Get("But-Automatic-Upgrades"))
	release.Architectures = strings.Fields(stanza.Get("Architectures"))
	release.Components = strings.Fields(stanza.Get("Components"))

	var err error
	if release.Date, err = ParseTime(stanza.Get("Date")); err != nil {
		return nil, err
	}
	if release.ValidUntil, err = ParseTime(stanza.Get("Valid-Until")); err != nil {
		return nil, err
	}

	for _, name := range stanza.Names() {
		algo, ok := checksumSections[strings.ToLower(strings.TrimSpace(name))]
		if !ok {
			continue
		}
		entries, err := ParseFileEntries(stanza.Get(name), algo)
		if err != nil {
			return nil, err
		}
		if release.byAlgo[algo] == nil {
			release.byAlgo[algo] = make(map[string]FileEntry, len(entries))
		}
		for _, entry := range entries {
			release.entries = append(release.entries, entry)
			release.byAlgo[algo][entry.Path] = entry
			existing, ok := release.byPath[entry.Path]
			if !ok {
				release.paths = append(release.paths, entry.Path)
			}
			if !ok || checksumRank(entry.Checksum.Type) > checksumRank(existing.Checksum.Type) {
				release.byPath[entry.Path] = entry
			}
		}
	}
	return release, nil
}

// ParseFileEntries 解析校验值分节或 Sources 的 Files/Checksums-* 字段：
// 每行是 "摘要 大小 文件名"。
func ParseFileEntries(value, algo string) ([]FileEntry, error) {
	var entries []FileEntry
	for _, line := range strings.Split(value, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 3 {
			return nil, fmt.Errorf("%w: %s 分节中的 %q 应为 \"摘要 大小 路径\"",
				ErrInvalidControl, algo, strings.TrimSpace(line))
		}
		size, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%w: %s 分节中的大小 %q 非法", ErrInvalidControl, algo, fields[1])
		}
		entries = append(entries, FileEntry{
			Path:     fields[2],
			Size:     size,
			Checksum: Checksum{Type: algo, Value: fields[0]},
		})
	}
	return entries, nil
}

// Field 返回 Release 文件中的原始字段值，字段名不区分大小写。
func (r *Release) Field(name string) string {
	if r == nil || r.stanza == nil {
		return ""
	}
	return r.stanza.Get(name)
}

// FieldNames 返回 Release 文件中出现的字段名（按原顺序）。
func (r *Release) FieldNames() []string {
	if r == nil || r.stanza == nil {
		return nil
	}
	return r.stanza.Names()
}

// FieldsMap 返回 Release 文件中的全部字段（键为小写字段名）。
func (r *Release) FieldsMap() map[string]string {
	if r == nil || r.stanza == nil {
		return nil
	}
	fields := make(map[string]string, r.stanza.Len())
	for _, name := range r.stanza.Names() {
		fields[strings.ToLower(name)] = r.stanza.Get(name)
	}
	return fields
}

// MarshalJSON 输出解析后的字段，并附带 Release 文件中的原始字段。
func (r *Release) MarshalJSON() ([]byte, error) {
	if r == nil {
		return []byte("null"), nil
	}
	type alias Release
	return json.Marshal(struct {
		*alias
		Fields map[string]string `json:"fields,omitempty"`
	}{(*alias)(r), r.FieldsMap()})
}

// Files 返回 Release 中记录的全部文件条目（按文件出现顺序，含各校验算法）。
func (r *Release) Files() []FileEntry {
	if r == nil {
		return nil
	}
	entries := make([]FileEntry, len(r.entries))
	copy(entries, r.entries)
	return entries
}

// FilesByStrength 按路径去重后返回文件条目：每个路径只保留最强算法
// （sha512 > sha256 > sha1 > md5）的那一条，顺序与文件首次出现的顺序一致。
// 展示仓库的索引清单时通常用这个方法，避免同一路径重复出现四次。
func (r *Release) FilesByStrength() []FileEntry {
	if r == nil {
		return nil
	}
	entries := make([]FileEntry, 0, len(r.paths))
	for _, path := range r.paths {
		if entry, ok := r.byPath[path]; ok {
			entries = append(entries, entry)
		}
	}
	return entries
}

// Lookup 按路径返回最强算法（sha512 > sha256 > sha1 > md5）对应的文件条目。
func (r *Release) Lookup(path string) (FileEntry, bool) {
	if r == nil {
		return FileEntry{}, false
	}
	entry, ok := r.byPath[path]
	return entry, ok
}

// LookupAlgorithm 按路径与算法返回文件条目。
func (r *Release) LookupAlgorithm(path, algo string) (FileEntry, bool) {
	if r == nil {
		return FileEntry{}, false
	}
	algo = strings.ToLower(strings.TrimSpace(algo))
	if normalized, ok := checksumSections[algo]; ok {
		algo = normalized
	}
	files, ok := r.byAlgo[algo]
	if !ok {
		return FileEntry{}, false
	}
	entry, ok := files[path]
	return entry, ok
}

// Has 判断 Release 中是否记录了指定路径（即索引是否真实存在）。
func (r *Release) Has(path string) bool {
	_, ok := r.Lookup(path)
	return ok
}

// Expired 判断仓库是否已超过 Valid-Until（未设置 Valid-Until 时永远返回 false）。
func (r *Release) Expired(now time.Time) bool {
	return r != nil && r.ValidUntil.IsSet() && now.After(r.ValidUntil.Time())
}

// ClearPGPArmor 剥离 InRelease 的明文签名包裹，返回签名保护的正文；
// 输入不是 PGP 明文签名时原样返回。
//
// InRelease 的格式（RFC 4880 cleartext signature framework）：
//
//	-----BEGIN PGP SIGNED MESSAGE-----
//	Hash: SHA256
//
//	Origin: Debian
//	…
//	-----BEGIN PGP SIGNATURE-----
//	…
//	-----END PGP SIGNATURE-----
func ClearPGPArmor(data []byte) []byte {
	text := string(data)
	trimmed := strings.TrimLeft(text, "\r\n \t")
	const begin = "-----BEGIN PGP SIGNED MESSAGE-----"
	if !strings.HasPrefix(trimmed, begin) {
		return data
	}
	body := trimmed[len(begin):]
	body = strings.TrimLeft(body, "\r\n")
	// 跳过 "Hash: SHA256" 等头部，直到第一个空行。
	if idx := strings.Index(body, "\r\n\r\n"); idx >= 0 {
		body = body[idx+4:]
	} else if idx := strings.Index(body, "\n\n"); idx >= 0 {
		body = body[idx+2:]
	}
	// 截断签名部分。
	if idx := strings.Index(body, "-----BEGIN PGP SIGNATURE-----"); idx >= 0 {
		body = body[:idx]
	}
	// 去掉 dash-escape（"- " 前缀）。
	var sb strings.Builder
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.HasPrefix(line, "- ") {
			line = line[2:]
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	return []byte(sb.String())
}

// parseYesNo 解析 Debian 的布尔写法：yes/no、true/false、1/0。
func parseYesNo(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "yes", "true", "1", "y":
		return true
	default:
		return false
	}
}
