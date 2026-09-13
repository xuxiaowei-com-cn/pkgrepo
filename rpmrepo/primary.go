package rpmrepo

import (
	"encoding/xml"
	"fmt"
	"io"
)

// ParsePrimary streams over primary.xml, calling fn once per parsed package.
//
// An error returned by fn stops parsing and is returned unchanged, which makes it easy to exit the
// iteration early. Because parsing is streaming, memory usage stays constant even for repositories
// with hundreds of thousands of packages.
func ParsePrimary(r io.Reader, fn func(*Package) error) error {
	if fn == nil {
		return nil
	}
	dec := xml.NewDecoder(r)
	dec.CharsetReader = charsetReader
	for {
		token, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("rpmrepo: parsing primary.xml failed: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "package" {
			continue
		}
		var pkg Package
		if err := dec.DecodeElement(&pkg, &start); err != nil {
			return fmt.Errorf("rpmrepo: parsing a package in primary.xml failed: %w", err)
		}
		if err := fn(&pkg); err != nil {
			return err
		}
	}
}

// PrimaryMeta holds the statistics found on the root element of primary.xml.
type PrimaryMeta struct {
	// Packages is the total number of packages in the repository.
	Packages int
}

// ParsePrimaryMeta reads only the attributes of the root element of primary.xml and does not parse
// any packages.
func ParsePrimaryMeta(r io.Reader) (*PrimaryMeta, error) {
	dec := xml.NewDecoder(r)
	dec.CharsetReader = charsetReader
	for {
		token, err := dec.Token()
		if err == io.EOF {
			return nil, fmt.Errorf("rpmrepo: primary.xml is missing the metadata element")
		}
		if err != nil {
			return nil, fmt.Errorf("rpmrepo: parsing primary.xml failed: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "metadata" {
			continue
		}
		meta := &PrimaryMeta{}
		for _, attr := range start.Attr {
			if attr.Name.Local != "packages" {
				continue
			}
			if _, err := fmt.Sscanf(attr.Value, "%d", &meta.Packages); err != nil {
				return nil, fmt.Errorf("rpmrepo: invalid packages attribute %q in primary.xml: %w", attr.Value, err)
			}
		}
		return meta, nil
	}
}
