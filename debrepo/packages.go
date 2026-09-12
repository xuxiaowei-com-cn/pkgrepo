package debrepo

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Size 是软件包的大小信息。
type Size struct {
	// File 是 .deb 文件大小（字节），对应索引中的 Size 字段。
	File int64 `json:"file"`
	// Installed 是安装后占用的磁盘空间（KiB），对应索引中的 Installed-Size 字段。
	Installed int64 `json:"installed"`
}

// Description 是软件包的描述：Synopsis 是首行摘要，Long 是完整描述。
type Description struct {
	Synopsis string `json:"synopsis,omitempty"`
	Long     string `json:"long,omitempty"`
}

// String 返回完整描述（摘要 + 长描述）。
func (d Description) String() string {
	if d.Long == "" {
		return d.Synopsis
	}
	if d.Synopsis == "" {
		return d.Long
	}
	return d.Synopsis + "\n" + d.Long
}

// IsZero 判断描述是否为空。
func (d Description) IsZero() bool { return d.Synopsis == "" && d.Long == "" }

// Package 是一个二进制软件包的元数据，对应 Packages 索引中的一个段落。
//
// 字段名与 Debian 控制文件保持一致；此外还包含由仓库自动填充的
// DownloadURL、RepoURL、RepoID、Suite、Component 等上下文信息。
type Package struct {
	// Name 是软件包名称。
	Name string `json:"name"`
	// Source 是源码包名称，SourceVersion 是 Source 字段中括号里的版本（通常为空）。
	Source        string `json:"source,omitempty"`
	SourceVersion string `json:"source_version,omitempty"`
	// Version 是版本号（epoch:upstream-revision）。
	Version Version `json:"version"`
	// Architecture 是架构，例如 amd64、arm64、all。
	Architecture string `json:"architecture"`
	// MultiArch 是 Multi-Arch 标记：same、foreign、allowed、no。
	MultiArch string `json:"multi_arch,omitempty"`
	// Essential 表示该包是系统必需包。
	Essential bool `json:"essential,omitempty"`
	// Priority 是优先级（required、important、standard、optional、extra）。
	Priority string `json:"priority,omitempty"`
	// Section 是软件分类（如 utils、net）。
	Section string `json:"section,omitempty"`
	// Description 是摘要与完整描述。
	Description Description `json:"description,omitempty"`
	// Maintainer 是维护者，OriginalMaintainer 是原始维护者（Ubuntu 的 XSBC-Original-Maintainer）。
	Maintainer         string `json:"maintainer,omitempty"`
	OriginalMaintainer string `json:"original_maintainer,omitempty"`
	// Homepage 是上游项目地址。
	Homepage string `json:"homepage,omitempty"`

	// Size 是包文件大小与安装后占用空间。
	Size Size `json:"size"`
	// Filename 是包文件相对仓库根的路径，例如
	// pool/main/n/nginx/nginx_1.22.1-9_amd64.deb。
	Filename string `json:"filename"`
	// DownloadURL 是解析后的绝对下载地址，由仓库加载时填充。
	DownloadURL string `json:"download_url,omitempty"`
	// Checksums 是包文件的指纹（MD5/SHA1/SHA256/SHA512）。
	Checksums Checksums `json:"checksums"`

	// 依赖关系，与 Debian 字段一一对应。
	Depends    Dependencies `json:"depends,omitempty"`
	PreDepends Dependencies `json:"pre_depends,omitempty"`
	Recommends Dependencies `json:"recommends,omitempty"`
	Suggests   Dependencies `json:"suggests,omitempty"`
	Breaks     Dependencies `json:"breaks,omitempty"`
	Conflicts  Dependencies `json:"conflicts,omitempty"`
	Provides   Dependencies `json:"provides,omitempty"`
	Replaces   Dependencies `json:"replaces,omitempty"`
	Enhances   Dependencies `json:"enhances,omitempty"`
	// BuiltUsing 是构建该包时用到的源码包（Built-Using 字段）。
	BuiltUsing Dependencies `json:"built_using,omitempty"`

	// Suite、Component 是该包所属的发行版与组件。
	Suite     string `json:"suite,omitempty"`
	Component string `json:"component,omitempty"`
	// RepoURL 是包所属仓库的根地址，RepoID 是仓库标识。
	RepoURL string `json:"repo_url,omitempty"`
	RepoID  string `json:"repo_id,omitempty"`

	// Fields 保存索引中的所有字段（键为小写字段名），便于读取 Tag、Task、
	// Bugs、Package-Type 等没有进入结构体的字段。
	Fields map[string]string `json:"fields,omitempty"`
}

// dependencyFields 是 Package 中需要解析为依赖关系的字段。
var packageDependencyFields = []struct {
	field string
	set   func(*Package, Dependencies)
}{
	{"Depends", func(p *Package, d Dependencies) { p.Depends = d }},
	{"Pre-Depends", func(p *Package, d Dependencies) { p.PreDepends = d }},
	{"Recommends", func(p *Package, d Dependencies) { p.Recommends = d }},
	{"Suggests", func(p *Package, d Dependencies) { p.Suggests = d }},
	{"Breaks", func(p *Package, d Dependencies) { p.Breaks = d }},
	{"Conflicts", func(p *Package, d Dependencies) { p.Conflicts = d }},
	{"Provides", func(p *Package, d Dependencies) { p.Provides = d }},
	{"Replaces", func(p *Package, d Dependencies) { p.Replaces = d }},
	{"Enhances", func(p *Package, d Dependencies) { p.Enhances = d }},
	{"Built-Using", func(p *Package, d Dependencies) { p.BuiltUsing = d }},
}

// ParsePackages 流式解析 Packages 索引，每解析出一个软件包就调用一次 fn。
//
// fn 返回的错误会中止解析并原样返回，便于在遍历过程中提前退出。
// 由于是流式解析，即使仓库有几十万个包，内存占用也保持不变。
func ParsePackages(r io.Reader, fn func(*Package) error) error {
	if fn == nil {
		return nil
	}
	return ParseStanzasFunc(r, func(stanza *Stanza) error {
		pkg, err := ParsePackageStanza(stanza)
		if err != nil {
			return err
		}
		return fn(pkg)
	})
}

// ParsePackageStanza 把 Packages 索引中的一个段落解析为 Package。
func ParsePackageStanza(stanza *Stanza) (*Package, error) {
	pkg := &Package{}
	pkg.Name = stanza.Get("Package")
	if pkg.Name == "" {
		return nil, fmt.Errorf("%w: 缺少 Package 字段", ErrInvalidPackage)
	}
	pkg.Source, pkg.SourceVersion = parseSourceField(stanza.Get("Source"))
	if pkg.Source == "" {
		pkg.Source = pkg.Name
	}
	pkg.Architecture = stanza.Get("Architecture")
	pkg.MultiArch = stanza.Get("Multi-Arch")
	pkg.Essential = parseYesNo(stanza.Get("Essential"))
	pkg.Priority = stanza.Get("Priority")
	pkg.Section = stanza.Get("Section")
	pkg.Description = parseDescription(stanza.Get("Description"))
	pkg.Maintainer = stanza.Get("Maintainer")
	pkg.OriginalMaintainer = firstNonEmpty(stanza.Get("Original-Maintainer"), stanza.Get("XSBC-Original-Maintainer"))
	pkg.Homepage = stanza.Get("Homepage")
	pkg.Filename = stanza.Get("Filename")
	pkg.Checksums = Checksums{
		MD5:    stanza.Get("MD5sum"),
		SHA1:   stanza.Get("SHA1"),
		SHA256: stanza.Get("SHA256"),
		SHA512: stanza.Get("SHA512"),
	}

	version := stanza.Get("Version")
	if version != "" {
		parsed, err := ParseVersion(version)
		if err != nil {
			return nil, fmt.Errorf("%w: %s %s", ErrInvalidPackage, pkg.Name, err)
		}
		pkg.Version = parsed
	}
	var err error
	if pkg.Size.File, err = parseOptionalInt(stanza.Get("Size")); err != nil {
		return nil, fmt.Errorf("%w: %s 的 Size 字段非法: %w", ErrInvalidPackage, pkg.Name, err)
	}
	if pkg.Size.Installed, err = parseOptionalInt(stanza.Get("Installed-Size")); err != nil {
		return nil, fmt.Errorf("%w: %s 的 Installed-Size 字段非法: %w", ErrInvalidPackage, pkg.Name, err)
	}

	for _, item := range packageDependencyFields {
		value := stanza.Get(item.field)
		if value == "" {
			continue
		}
		deps, err := ParseDependencies(value)
		if err != nil {
			return nil, fmt.Errorf("%w: %s 的 %s 字段: %w", ErrInvalidPackage, pkg.Name, item.field, err)
		}
		item.set(pkg, deps)
	}

	pkg.Fields = make(map[string]string, stanza.Len())
	for _, name := range stanza.Names() {
		pkg.Fields[strings.ToLower(name)] = stanza.Get(name)
	}
	return pkg, nil
}

// parseSourceField 解析 "Source" 字段：可能只是名字，也可能带 "(version)"。
func parseSourceField(raw string) (name, version string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	if open := strings.IndexByte(raw, '('); open >= 0 {
		if closeIdx := strings.IndexByte(raw[open:], ')'); closeIdx >= 0 {
			version = strings.TrimSpace(raw[open+1 : open+closeIdx])
			name = strings.TrimSpace(raw[:open])
			return name, version
		}
	}
	return raw, ""
}

// parseDescription 拆分 Debian 的多行描述：首行是摘要，其余行是长描述；
// 只包含 "." 的行表示空行。
func parseDescription(raw string) Description {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Description{}
	}
	lines := strings.Split(raw, "\n")
	desc := Description{Synopsis: strings.TrimSpace(lines[0])}
	long := make([]string, 0, len(lines)-1)
	for _, line := range lines[1:] {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "." {
			long = append(long, "")
			continue
		}
		long = append(long, line)
	}
	// 去掉长描述首尾的空行。
	for len(long) > 0 && strings.TrimSpace(long[0]) == "" {
		long = long[1:]
	}
	for len(long) > 0 && strings.TrimSpace(long[len(long)-1]) == "" {
		long = long[:len(long)-1]
	}
	desc.Long = strings.Join(long, "\n")
	return desc
}

// parseOptionalInt 解析可选的整数字段，空字符串返回 0。
func parseOptionalInt(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	return strconv.ParseInt(raw, 10, 64)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// SourceName 返回源码包名称（Source 字段缺省时就是包名）。
func (p *Package) SourceName() string {
	if p.Source != "" {
		return p.Source
	}
	return p.Name
}

// IsArchitectureIndependent 判断是否为架构无关包（Architecture: all）。
func (p *Package) IsArchitectureIndependent() bool { return p.Architecture == "all" }

// Checksum 返回最强可用的指纹（sha512 > sha256 > sha1 > md5）。
func (p *Package) Checksum() (Checksum, bool) { return p.Checksums.Strongest() }

// BaseFilename 返回包文件名（Filename 的最后一段）。
func (p *Package) BaseFilename() string {
	if idx := strings.LastIndexByte(p.Filename, '/'); idx >= 0 {
		return p.Filename[idx+1:]
	}
	return p.Filename
}

// ID 返回形如 "nginx_1.22.1-9_amd64" 的标识。
func (p *Package) ID() string {
	parts := []string{p.Name}
	if !p.Version.IsZero() {
		parts = append(parts, p.Version.String())
	}
	if p.Architecture != "" {
		parts = append(parts, p.Architecture)
	}
	return strings.Join(parts, "_")
}

// String 返回包的标识（与 ID 相同）。
func (p *Package) String() string { return p.ID() }

// Field 返回索引中的原始字段值，字段名不区分大小写。
func (p *Package) Field(name string) string { return p.Fields[strings.ToLower(name)] }

// HasField 判断索引中是否存在指定字段。
func (p *Package) HasField(name string) bool {
	_, ok := p.Fields[strings.ToLower(name)]
	return ok
}

// ProvidesPackage 判断该包是否提供指定虚拟包（Provides 字段）。
func (p *Package) ProvidesPackage(name string) bool { return p.Provides.Has(name) }

// DependsOn 判断该包是否直接依赖指定包（Depends 字段，含替代方案）。
func (p *Package) DependsOn(name string) bool { return p.Depends.Has(name) }
