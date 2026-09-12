package debrepo

import (
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
)

// Source 是一个源码包的元数据，对应 Sources 索引中的一个段落。
type Source struct {
	// Name 是源码包名称（Package 字段）。
	Name string `json:"name"`
	// Binary 是该源码包生成的二进制包名列表。
	Binary []string `json:"binary,omitempty"`
	// Version 是版本号。
	Version Version `json:"version"`
	// Architecture 是源码包支持的架构（any、all 或具体架构列表）。
	Architecture string `json:"architecture,omitempty"`
	// Maintainer、Uploaders 是维护者与上传者。
	Maintainer string   `json:"maintainer,omitempty"`
	Uploaders  []string `json:"uploaders,omitempty"`
	// Homepage、Section、Priority 同二进制包。
	Homepage string `json:"homepage,omitempty"`
	Section  string `json:"section,omitempty"`
	Priority string `json:"priority,omitempty"`
	// StandardsVersion 是打包时遵循的 Debian 政策版本。
	StandardsVersion string `json:"standards_version,omitempty"`
	// Format 是源码包格式，例如 "3.0 (quilt)"。
	Format string `json:"format,omitempty"`
	// Directory 是源码包文件所在目录（相对仓库根），例如 pool/main/n/nginx。
	Directory string `json:"directory,omitempty"`
	// Files 是源码包中的文件（MD5），Checksums 是各算法下的文件列表。
	Files     []FileEntry            `json:"files,omitempty"`
	Checksums map[string][]FileEntry `json:"checksums,omitempty"`

	// 构建依赖关系。
	BuildDepends        Dependencies `json:"build_depends,omitempty"`
	BuildDependsIndep   Dependencies `json:"build_depends_indep,omitempty"`
	BuildDependsArch    Dependencies `json:"build_depends_arch,omitempty"`
	BuildConflicts      Dependencies `json:"build_conflicts,omitempty"`
	BuildConflictsIndep Dependencies `json:"build_conflicts_indep,omitempty"`
	BuildConflictsArch  Dependencies `json:"build_conflicts_arch,omitempty"`

	// 版本控制地址。
	VcsGit     string `json:"vcs_git,omitempty"`
	VcsBrowser string `json:"vcs_browser,omitempty"`
	VcsSvn     string `json:"vcs_svn,omitempty"`
	VcsHg      string `json:"vcs_hg,omitempty"`
	VcsBzr     string `json:"vcs_bzr,omitempty"`
	// Testsuite 是 autopkgtest 相关的声明。
	Testsuite string `json:"testsuite,omitempty"`

	// Suite、Component 是该源码包所属的发行版与组件。
	Suite     string `json:"suite,omitempty"`
	Component string `json:"component,omitempty"`
	// RepoURL 是包所属仓库的根地址，RepoID 是仓库标识。
	RepoURL string `json:"repo_url,omitempty"`
	RepoID  string `json:"repo_id,omitempty"`

	// Fields 保存索引中的所有字段（键为小写字段名）。
	Fields map[string]string `json:"fields,omitempty"`
}

// sourceDependencyFields 是 Source 中需要解析为依赖关系的字段。
var sourceDependencyFields = []struct {
	field string
	set   func(*Source, Dependencies)
}{
	{"Build-Depends", func(s *Source, d Dependencies) { s.BuildDepends = d }},
	{"Build-Depends-Indep", func(s *Source, d Dependencies) { s.BuildDependsIndep = d }},
	{"Build-Depends-Arch", func(s *Source, d Dependencies) { s.BuildDependsArch = d }},
	{"Build-Conflicts", func(s *Source, d Dependencies) { s.BuildConflicts = d }},
	{"Build-Conflicts-Indep", func(s *Source, d Dependencies) { s.BuildConflictsIndep = d }},
	{"Build-Conflicts-Arch", func(s *Source, d Dependencies) { s.BuildConflictsArch = d }},
}

// sourceChecksumFields 是 Sources 索引中的文件校验字段。
var sourceChecksumFields = []struct {
	field string
	algo  string
}{
	{"Files", "md5"},
	{"Checksums-Sha1", "sha1"},
	{"Checksums-Sha256", "sha256"},
	{"Checksums-Sha512", "sha512"},
}

// ParseSources 流式解析 Sources 索引，每解析出一个源码包就调用一次 fn。
func ParseSources(r io.Reader, fn func(*Source) error) error {
	if fn == nil {
		return nil
	}
	return ParseStanzasFunc(r, func(stanza *Stanza) error {
		source, err := ParseSourceStanza(stanza)
		if err != nil {
			return err
		}
		return fn(source)
	})
}

// ParseSourceStanza 把 Sources 索引中的一个段落解析为 Source。
func ParseSourceStanza(stanza *Stanza) (*Source, error) {
	source := &Source{Checksums: make(map[string][]FileEntry, 4)}
	source.Name = stanza.Get("Package")
	if source.Name == "" {
		return nil, fmt.Errorf("%w: 缺少 Package 字段", ErrInvalidPackage)
	}
	source.Binary = splitList(stanza.Get("Binary"))
	source.Architecture = stanza.Get("Architecture")
	source.Maintainer = stanza.Get("Maintainer")
	source.Uploaders = splitCommaList(stanza.Get("Uploaders"))
	source.Homepage = stanza.Get("Homepage")
	source.Section = stanza.Get("Section")
	source.Priority = stanza.Get("Priority")
	source.StandardsVersion = stanza.Get("Standards-Version")
	source.Format = stanza.Get("Format")
	source.Directory = stanza.Get("Directory")
	source.VcsGit = stanza.Get("Vcs-Git")
	source.VcsBrowser = stanza.Get("Vcs-Browser")
	source.VcsSvn = stanza.Get("Vcs-Svn")
	source.VcsHg = stanza.Get("Vcs-Hg")
	source.VcsBzr = stanza.Get("Vcs-Bzr")
	source.Testsuite = stanza.Get("Testsuite")

	version := stanza.Get("Version")
	if version != "" {
		parsed, err := ParseVersion(version)
		if err != nil {
			return nil, fmt.Errorf("%w: %s %s", ErrInvalidPackage, source.Name, err)
		}
		source.Version = parsed
	}

	for _, item := range sourceChecksumFields {
		value := stanza.Get(item.field)
		if value == "" {
			continue
		}
		entries, err := ParseFileEntries(value, item.algo)
		if err != nil {
			return nil, fmt.Errorf("%w: %s 的 %s 字段: %w", ErrInvalidPackage, source.Name, item.field, err)
		}
		source.Checksums[item.algo] = entries
		if item.algo == "md5" {
			source.Files = entries
		}
	}

	for _, item := range sourceDependencyFields {
		value := stanza.Get(item.field)
		if value == "" {
			continue
		}
		deps, err := ParseDependencies(value)
		if err != nil {
			return nil, fmt.Errorf("%w: %s 的 %s 字段: %w", ErrInvalidPackage, source.Name, item.field, err)
		}
		item.set(source, deps)
	}

	source.Fields = make(map[string]string, stanza.Len())
	for _, name := range stanza.Names() {
		source.Fields[strings.ToLower(name)] = stanza.Get(name)
	}
	return source, nil
}

// splitList 按空白与逗号切分列表（Binary 字段可能用空格或逗号分隔）。
func splitList(value string) []string {
	value = strings.ReplaceAll(value, "\n", " ")
	var items []string
	for _, field := range strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	}) {
		if field != "" {
			items = append(items, field)
		}
	}
	return items
}

// splitCommaList 按逗号切分多行列表（Uploaders 等字段）。
func splitCommaList(value string) []string {
	value = strings.ReplaceAll(value, "\n", " ")
	var items []string
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			items = append(items, trimmed)
		}
	}
	return items
}

// ID 返回形如 "nginx_1.22.1-9" 的源码包标识。
func (s *Source) ID() string {
	if s.Version.IsZero() {
		return s.Name
	}
	return s.Name + "_" + s.Version.String()
}

// String 返回源码包标识（与 ID 相同）。
func (s *Source) String() string { return s.ID() }

// Field 返回索引中的原始字段值，字段名不区分大小写。
func (s *Source) Field(name string) string { return s.Fields[strings.ToLower(name)] }

// HasField 判断索引中是否存在指定字段。
func (s *Source) HasField(name string) bool {
	_, ok := s.Fields[strings.ToLower(name)]
	return ok
}

// ChecksumOf 返回源码包中某个文件最强算法的校验值。
func (s *Source) ChecksumOf(name string) (Checksum, bool) {
	for _, algo := range []string{"sha512", "sha256", "sha1", "md5"} {
		for _, entry := range s.Checksums[algo] {
			if entry.Path == name {
				return entry.Checksum, true
			}
		}
	}
	return Checksum{}, false
}

// DSC 返回源码包中的 .dsc 文件条目。
func (s *Source) DSC() (FileEntry, bool) {
	for _, entry := range s.Files {
		if strings.HasSuffix(entry.Path, ".dsc") {
			return entry, true
		}
	}
	return FileEntry{}, false
}

// FileURL 返回源码包中某个文件（例如 .dsc 或 .orig.tar.gz）的下载地址。
// 需要仓库已填充 RepoURL 与 Directory。
func (s *Source) FileURL(name string) (string, error) {
	if s.RepoURL == "" {
		return "", fmt.Errorf("debrepo: 源码包 %s 没有仓库地址", s.ID())
	}
	base, err := url.Parse(s.RepoURL)
	if err != nil {
		return "", fmt.Errorf("debrepo: 非法的仓库地址 %q: %w", s.RepoURL, err)
	}
	if s.Directory == "" {
		return "", fmt.Errorf("debrepo: 源码包 %s 没有 Directory 字段", s.ID())
	}
	reference := &url.URL{Path: path.Join(s.Directory, name)}
	return base.ResolveReference(reference).String(), nil
}
