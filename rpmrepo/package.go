package rpmrepo

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// UnixTime 是 RPM 元数据中的 Unix 时间戳（秒）。
type UnixTime int64

// Time 把时间戳转换为 UTC 的 time.Time，零值表示元数据未提供该时间。
func (t UnixTime) Time() time.Time { return time.Unix(int64(t), 0).UTC() }

// IsZero 判断时间戳是否为空。
func (t UnixTime) IsZero() bool { return t == 0 }

// String 返回 RFC3339 格式的时间，为空时返回空字符串。
func (t UnixTime) String() string {
	if t == 0 {
		return ""
	}
	return t.Time().Format(time.RFC3339)
}

// MarshalJSON 把时间戳输出为 RFC3339 字符串，零值输出 null。
func (t UnixTime) MarshalJSON() ([]byte, error) {
	if t == 0 {
		return []byte("null"), nil
	}
	return json.Marshal(t.String())
}

// UnmarshalXMLAttr 解析属性形式的时间戳，例如 <time file="1615500000"/>。
func (t *UnixTime) UnmarshalXMLAttr(attr xml.Attr) error { return t.parse(attr.Value) }

// UnmarshalXML 解析元素形式的时间戳，例如 <timestamp>1615500000</timestamp>。
func (t *UnixTime) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	var raw string
	if err := d.DecodeElement(&raw, &start); err != nil {
		return err
	}
	return t.parse(raw)
}

func (t *UnixTime) parse(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "-" {
		*t = 0
		return nil
	}
	sec, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fmt.Errorf("rpmrepo: 非法的时间戳 %q: %w", raw, err)
	}
	*t = UnixTime(sec)
	return nil
}

// XMLBool 兼容 RPM 元数据中的多种布尔写法：YES/NO、true/false、1/0。
type XMLBool bool

// UnmarshalXMLAttr 解析属性形式的布尔值。
func (b *XMLBool) UnmarshalXMLAttr(attr xml.Attr) error {
	switch strings.ToLower(strings.TrimSpace(attr.Value)) {
	case "", "no", "n", "false", "0":
		*b = false
	case "yes", "y", "true", "1":
		*b = true
	default:
		return fmt.Errorf("rpmrepo: 非法的布尔值 %q", attr.Value)
	}
	return nil
}

// Checksum 是软件包的校验值（指纹），例如 sha256 摘要。
type Checksum struct {
	// Type 是算法名，常见为 sha256、sha1、sha512、md5。
	Type string `xml:"type,attr" json:"type"`
	// Value 是十六进制的摘要值。
	Value string `xml:",chardata" json:"value"`
	// PkgID 表示该摘要是否可作为包的唯一标识（primary.xml 中 pkgid="YES"）。
	PkgID XMLBool `xml:"pkgid,attr" json:"pkg_id,omitempty"`
}

// String 返回 "type:value"，校验值为空时返回空字符串。
func (c Checksum) String() string {
	if c.Value == "" {
		return ""
	}
	return c.Type + ":" + c.Value
}

// Location 是软件包文件在仓库中的位置。
type Location struct {
	// Href 是相对仓库根的路径，例如 Packages/n/nginx-1.24.0-1.el9.x86_64.rpm。
	Href string `xml:"href,attr" json:"href"`
	// Base 是可选的 xml:base 属性，用于覆盖相对路径的基准地址。
	Base string `xml:"base,attr" json:"base,omitempty"`
}

// Size 是软件包文件的各项大小（字节）。
type Size struct {
	// Package 是 rpm 文件大小。
	Package int64 `xml:"package,attr" json:"package"`
	// Installed 是安装后占用的磁盘空间。
	Installed int64 `xml:"installed,attr" json:"installed"`
	// Archive 是包内归档文件的总大小。
	Archive int64 `xml:"archive,attr" json:"archive"`
}

// Time 是软件包的入库时间与构建时间。
type Time struct {
	// File 是软件包加入仓库的时间。
	File UnixTime `xml:"file,attr" json:"file"`
	// Build 是软件包的构建时间。
	Build UnixTime `xml:"build,attr" json:"build"`
}

// HeaderRange 是 rpm 头部在文件中的字节区间，可用于只下载头部做增量分析。
type HeaderRange struct {
	Start int64 `xml:"start,attr" json:"start"`
	End   int64 `xml:"end,attr" json:"end"`
}

// Dependency 是一条依赖关系（Provides/Requires/Conflicts/Obsoletes 等）。
type Dependency struct {
	// Name 是能力名，例如 nginx、libc.so.6()(64bit)。
	Name string `xml:"name,attr" json:"name"`
	// Flags 是版本约束，取值为 EQ、LT、LE、GT、GE 之一，为空表示不限制版本。
	Flags string `xml:"flags,attr" json:"flags,omitempty"`
	// Epoch、Version、Release 是约束涉及的版本，仅在带版本约束时出现。
	Epoch   string `xml:"epoch,attr" json:"epoch,omitempty"`
	Version string `xml:"ver,attr" json:"version,omitempty"`
	Release string `xml:"rel,attr" json:"release,omitempty"`
	// Pre 表示该依赖在安装前就需满足。
	Pre XMLBool `xml:"pre,attr" json:"pre,omitempty"`
}

// EVR 返回依赖的版本约束。
func (d Dependency) EVR() EVR {
	return EVR{Epoch: d.Epoch, Version: d.Version, Release: d.Release}
}

// IsVersioned 判断依赖是否带版本约束。
func (d Dependency) IsVersioned() bool { return d.Flags != "" || d.Version != "" }

// String 返回可读的依赖描述，例如 "nginx >= 1.24.0-1.el9"。
func (d Dependency) String() string {
	if !d.IsVersioned() {
		return d.Name
	}
	return d.Name + " " + d.Operator() + " " + d.EVR().String()
}

// Operator 把 rpm 的 flags 转换为 ">=" 这样的符号，无版本约束时返回空字符串。
func (d Dependency) Operator() string {
	switch strings.ToUpper(d.Flags) {
	case "EQ":
		return "="
	case "LT":
		return "<"
	case "LE":
		return "<="
	case "GT":
		return ">"
	case "GE":
		return ">="
	default:
		return ""
	}
}

// Format 是 rpm 头部中的扩展元数据。
type Format struct {
	License     string      `xml:"license" json:"license,omitempty"`
	Vendor      string      `xml:"vendor" json:"vendor,omitempty"`
	Group       string      `xml:"group" json:"group,omitempty"`
	BuildHost   string      `xml:"buildhost" json:"build_host,omitempty"`
	SourceRPM   string      `xml:"sourcerpm" json:"source_rpm,omitempty"`
	HeaderRange HeaderRange `xml:"header-range" json:"header_range"`

	Provides    []Dependency `xml:"provides>entry" json:"provides,omitempty"`
	Requires    []Dependency `xml:"requires>entry" json:"requires,omitempty"`
	Conflicts   []Dependency `xml:"conflicts>entry" json:"conflicts,omitempty"`
	Obsoletes   []Dependency `xml:"obsoletes>entry" json:"obsoletes,omitempty"`
	Recommends  []Dependency `xml:"recommends>entry" json:"recommends,omitempty"`
	Suggests    []Dependency `xml:"suggests>entry" json:"suggests,omitempty"`
	Supplements []Dependency `xml:"supplements>entry" json:"supplements,omitempty"`
	Enhances    []Dependency `xml:"enhances>entry" json:"enhances,omitempty"`
}

// Package 是一个软件包的元数据，对应 primary.xml 中的一个 <package> 元素。
type Package struct {
	// Type 通常是 rpm。
	Type string `xml:"type,attr" json:"type"`
	// Name 是软件包名称。
	Name string `xml:"name" json:"name"`
	// Arch 是架构，例如 x86_64、aarch64、noarch、src。
	Arch string `xml:"arch" json:"arch"`
	// Version 是版本信息（epoch/ver/rel）。
	Version EVR `xml:"version" json:"version"`
	// Checksum 是包文件的校验值（指纹）。
	Checksum Checksum `xml:"checksum" json:"checksum"`
	// Summary 是软件包摘要。
	Summary string `xml:"summary" json:"summary,omitempty"`
	// Description 是软件包描述。
	Description string `xml:"description" json:"description,omitempty"`
	// Packager 是打包者。
	Packager string `xml:"packager" json:"packager,omitempty"`
	// URL 是上游项目地址。
	URL string `xml:"url" json:"url,omitempty"`
	// Time 是入库与构建时间。
	Time Time `xml:"time" json:"time"`
	// Size 是包文件 / 安装后 / 归档的大小。
	Size Size `xml:"size" json:"size"`
	// Location 是包文件在仓库中的相对路径。
	Location Location `xml:"location" json:"location"`
	// Format 是 rpm 头部的扩展元数据，包含各项依赖。
	Format Format `xml:"format" json:"format"`

	// DownloadURL 是解析后的绝对下载地址，由仓库加载时填充。
	DownloadURL string `xml:"-" json:"download_url,omitempty"`
	// RepoURL 是包所属仓库的根地址。
	RepoURL string `xml:"-" json:"repo_url,omitempty"`
	// RepoID 是包所属仓库的标识。
	RepoID string `xml:"-" json:"repo_id,omitempty"`
}

// NEVRA 返回包的唯一标识：name-epoch:version-release.arch。
func (p *Package) NEVRA() string {
	return p.Name + "-" + p.Version.String() + "." + p.Arch
}

// Filename 返回包文件的名字，例如 nginx-1.24.0-1.el9.x86_64.rpm。
func (p *Package) Filename() string {
	base := p.Location.Href
	if idx := strings.LastIndexByte(base, '/'); idx >= 0 {
		base = base[idx+1:]
	}
	if unescaped, err := url.PathUnescape(base); err == nil {
		return unescaped
	}
	return base
}

// IsSource 判断是否为源码包。
func (p *Package) IsSource() bool { return p.Arch == "src" || p.Arch == "nosrc" }

// Provides 判断该包是否提供指定能力。
func (p *Package) Provides(name string) bool { return hasDependency(p.Format.Provides, name) }

// Requires 判断该包是否依赖指定能力。
func (p *Package) Requires(name string) bool { return hasDependency(p.Format.Requires, name) }

func hasDependency(deps []Dependency, name string) bool {
	for _, dep := range deps {
		if dep.Name == name {
			return true
		}
	}
	return false
}

// String 返回包的 NEVRA。
func (p *Package) String() string { return p.NEVRA() }
