package rpmrepo

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// 常见的 repomd.xml 元数据类型。
const (
	DataTypePrimary        = "primary"
	DataTypeFilelists      = "filelists"
	DataTypeOther          = "other"
	DataTypePrimaryDB      = "primary_db"
	DataTypeFilelistsDB    = "filelists_db"
	DataTypeOtherDB        = "other_db"
	DataTypeGroup          = "group"
	DataTypeGroupGz        = "group_gz"
	DataTypeUpdateInfo     = "updateinfo"
	DataTypeModules        = "modules"
	DataTypePrestoDelta    = "prestodelta"
	DataTypeProductID      = "productid"
	DataTypeComps          = "comps"
	DataTypeMirrors        = "mirrors"
	DataTypeOrigin         = "origin"
	DataTypeDistroTags     = "distro_tags"
	DataTypeUpdateInfoZstd = "updateinfo_zstd"
)

// RepoMD 是 repodata/repomd.xml 的解析结果：仓库元数据的索引。
type RepoMD struct {
	XMLName  xml.Name     `xml:"repomd" json:"-"`
	Revision string       `xml:"revision" json:"revision"`
	Data     []RepoMDData `xml:"data" json:"data"`
}

// RepoMDData 描述 repomd.xml 中的一个元数据文件。
type RepoMDData struct {
	// Type 是元数据类型，例如 primary、filelists、other、primary_db。
	Type string `xml:"type,attr" json:"type"`
	// Checksum 是元数据文件（压缩后）的校验值。
	Checksum Checksum `xml:"checksum" json:"checksum"`
	// OpenChecksum 是元数据解压后的校验值。
	OpenChecksum Checksum `xml:"open-checksum" json:"open_checksum"`
	// Location 是元数据文件的相对路径。
	Location Location `xml:"location" json:"location"`
	// Timestamp 是元数据的生成时间。
	Timestamp UnixTime `xml:"timestamp" json:"timestamp"`
	// Size 是元数据文件（压缩后）的大小。
	Size int64 `xml:"size" json:"size"`
	// OpenSize 是元数据解压后的大小。
	OpenSize int64 `xml:"open-size" json:"open_size"`
	// DatabaseVersion 是 sqlite 数据库（*_db）的版本。
	DatabaseVersion string `xml:"database_version" json:"database_version,omitempty"`
}

// ParseRepoMD 解析 repodata/repomd.xml。
func ParseRepoMD(r io.Reader) (*RepoMD, error) {
	var repomd RepoMD
	dec := xml.NewDecoder(r)
	dec.CharsetReader = charsetReader
	if err := dec.Decode(&repomd); err != nil {
		return nil, fmt.Errorf("rpmrepo: 解析 repomd.xml 失败: %w", err)
	}
	return &repomd, nil
}

// DataByType 按类型查找元数据，类型名不区分大小写。
func (m *RepoMD) DataByType(dataType string) (*RepoMDData, bool) {
	if m == nil {
		return nil, false
	}
	for i := range m.Data {
		if strings.EqualFold(m.Data[i].Type, dataType) {
			return &m.Data[i], true
		}
	}
	return nil, false
}

// Primary 返回 primary 元数据，仓库中不存在时返回 ErrPrimaryNotFound。
func (m *RepoMD) Primary() (*RepoMDData, error) {
	data, ok := m.DataByType(DataTypePrimary)
	if !ok {
		return nil, ErrPrimaryNotFound
	}
	return data, nil
}

// Types 返回仓库包含的元数据类型列表，保持 repomd.xml 中的顺序。
func (m *RepoMD) Types() []string {
	if m == nil {
		return nil
	}
	types := make([]string, 0, len(m.Data))
	for i := range m.Data {
		types = append(types, m.Data[i].Type)
	}
	return types
}
