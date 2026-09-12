package rpmrepo

import (
	"encoding/xml"
	"fmt"
	"io"
)

// ParsePrimary 流式解析 primary.xml，每解析出一个软件包就调用一次 fn。
//
// fn 返回的错误会中止解析并原样返回，便于在遍历过程中提前退出。
// 由于是流式解析，即使仓库有几十万个包，内存占用也保持不变。
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
			return fmt.Errorf("rpmrepo: 解析 primary.xml 失败: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "package" {
			continue
		}
		var pkg Package
		if err := dec.DecodeElement(&pkg, &start); err != nil {
			return fmt.Errorf("rpmrepo: 解析 primary.xml 中的软件包失败: %w", err)
		}
		if err := fn(&pkg); err != nil {
			return err
		}
	}
}

// PrimaryMeta 是 primary.xml 根元素上的统计信息。
type PrimaryMeta struct {
	// Packages 是仓库中的软件包总数。
	Packages int
}

// ParsePrimaryMeta 只读取 primary.xml 根元素的属性，不解析软件包。
func ParsePrimaryMeta(r io.Reader) (*PrimaryMeta, error) {
	dec := xml.NewDecoder(r)
	dec.CharsetReader = charsetReader
	for {
		token, err := dec.Token()
		if err == io.EOF {
			return nil, fmt.Errorf("rpmrepo: primary.xml 中缺少 metadata 元素")
		}
		if err != nil {
			return nil, fmt.Errorf("rpmrepo: 解析 primary.xml 失败: %w", err)
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
				return nil, fmt.Errorf("rpmrepo: primary.xml 的 packages 属性非法 %q: %w", attr.Value, err)
			}
		}
		return meta, nil
	}
}
