package rpmrepo

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// Common repomd.xml metadata types.
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

// RepoMD is the parse result of repodata/repomd.xml: an index of the repository metadata.
type RepoMD struct {
	XMLName  xml.Name     `xml:"repomd" json:"-"`
	Revision string       `xml:"revision" json:"revision"`
	Data     []RepoMDData `xml:"data" json:"data"`
}

// RepoMDData describes a single metadata file in repomd.xml.
type RepoMDData struct {
	// Type is the metadata type, for example primary, filelists, other, or primary_db.
	Type string `xml:"type,attr" json:"type"`
	// Checksum is the checksum of the metadata file (after compression).
	Checksum Checksum `xml:"checksum" json:"checksum"`
	// OpenChecksum is the checksum of the decompressed metadata.
	OpenChecksum Checksum `xml:"open-checksum" json:"open_checksum"`
	// Location is the relative path of the metadata file.
	Location Location `xml:"location" json:"location"`
	// Timestamp is the creation time of the metadata.
	Timestamp UnixTime `xml:"timestamp" json:"timestamp"`
	// Size is the size of the metadata file (after compression).
	Size int64 `xml:"size" json:"size"`
	// OpenSize is the size of the decompressed metadata.
	OpenSize int64 `xml:"open-size" json:"open_size"`
	// DatabaseVersion is the version of the sqlite database (*_db).
	DatabaseVersion string `xml:"database_version" json:"database_version,omitempty"`
}

// ParseRepoMD parses repodata/repomd.xml.
func ParseRepoMD(r io.Reader) (*RepoMD, error) {
	var repomd RepoMD
	dec := xml.NewDecoder(r)
	dec.CharsetReader = charsetReader
	if err := dec.Decode(&repomd); err != nil {
		return nil, fmt.Errorf("rpmrepo: parsing repomd.xml failed: %w", err)
	}
	return &repomd, nil
}

// DataByType looks up metadata by type; the type name is case-insensitive.
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

// Primary returns the primary metadata, or ErrPrimaryNotFound when it is absent from the repository.
func (m *RepoMD) Primary() (*RepoMDData, error) {
	data, ok := m.DataByType(DataTypePrimary)
	if !ok {
		return nil, ErrPrimaryNotFound
	}
	return data, nil
}

// Types returns the list of metadata types contained in the repository, preserving the order used in
// repomd.xml.
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
