package rpmrepo

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// 真实 repomd.xml 的解析在 realrepo_test.go 中验证，这里覆盖字段细节与错误分支。

const repomdFixture = `<?xml version="1.0" encoding="UTF-8"?>
<repomd xmlns="http://linux.duke.edu/metadata/repo" xmlns:rpm="http://linux.duke.edu/metadata/rpm">
  <revision>1700000000</revision>
  <data type="primary">
    <checksum type="sha256">aaa</checksum>
    <open-checksum type="sha256">bbb</open-checksum>
    <location href="repodata/aaa-primary.xml.gz"/>
    <timestamp>1700000000</timestamp>
    <size>42531</size>
    <open-size>883960</open-size>
  </data>
  <data type="primary_db">
    <checksum type="sha256">ccc</checksum>
    <location href="repodata/ccc-primary.sqlite.bz2"/>
    <timestamp>1700000100</timestamp>
    <database_version>10</database_version>
    <size>155707</size>
  </data>
</repomd>`

func TestParseRepoMDFields(t *testing.T) {
	repomd, err := ParseRepoMD(strings.NewReader(repomdFixture))
	if err != nil {
		t.Fatalf("ParseRepoMD 失败: %v", err)
	}
	if repomd.Revision != "1700000000" {
		t.Errorf("revision 为 %q", repomd.Revision)
	}
	if got := strings.Join(repomd.Types(), ","); got != "primary,primary_db" {
		t.Errorf("元数据类型为 %q", got)
	}

	primary, err := repomd.Primary()
	if err != nil {
		t.Fatalf("Primary 失败: %v", err)
	}
	if primary.Checksum.Value != "aaa" || primary.OpenChecksum.Value != "bbb" {
		t.Errorf("primary 校验值不符合预期: %+v", primary)
	}
	if primary.Size != 42531 || primary.OpenSize != 883960 {
		t.Errorf("primary 大小不符合预期: size=%d open-size=%d", primary.Size, primary.OpenSize)
	}
	if got, want := primary.Timestamp.Time(), time.Unix(1700000000, 0).UTC(); !got.Equal(want) {
		t.Errorf("primary 时间为 %v，期望 %v", got, want)
	}

	// 类型名不区分大小写。
	primaryDB, ok := repomd.DataByType("PRIMARY_DB")
	if !ok {
		t.Fatal("没有找到 primary_db 元数据")
	}
	if primaryDB.DatabaseVersion != "10" {
		t.Errorf("database_version 为 %q，期望 10", primaryDB.DatabaseVersion)
	}
	if primaryDB.OpenChecksum.Value != "" {
		t.Errorf("primary_db 不应有 open-checksum: %+v", primaryDB.OpenChecksum)
	}
	if got, want := primaryDB.Timestamp.Time(), time.Unix(1700000100, 0).UTC(); !got.Equal(want) {
		t.Errorf("primary_db 时间为 %v，期望 %v", got, want)
	}
	if primaryDB.Timestamp.IsZero() {
		t.Error("时间戳不应为 0")
	}

	checksum := primary.Checksum
	if checksum.String() != "sha256:aaa" {
		t.Errorf("Checksum.String() 为 %q", checksum.String())
	}
}

func TestRepoMDPrimaryMissing(t *testing.T) {
	const document = `<?xml version="1.0" encoding="UTF-8"?>
<repomd><data type="other"><location href="repodata/other.xml.gz"/></data></repomd>`
	repomd, err := ParseRepoMD(strings.NewReader(document))
	if err != nil {
		t.Fatalf("ParseRepoMD 失败: %v", err)
	}
	if _, err := repomd.Primary(); !errors.Is(err, ErrPrimaryNotFound) {
		t.Errorf("错误为 %v，期望 ErrPrimaryNotFound", err)
	}
	if _, ok := repomd.DataByType(DataTypePrimary); ok {
		t.Error("不应找到 primary 元数据")
	}
}

func TestUnixTimeErrors(t *testing.T) {
	const document = `<?xml version="1.0" encoding="UTF-8"?>
<repomd><data type="primary"><timestamp>不是时间</timestamp>
<location href="repodata/primary.xml.gz"/></data></repomd>`
	if _, err := ParseRepoMD(strings.NewReader(document)); err == nil {
		t.Fatal("非法时间戳应当报错")
	}
}

func TestUnixTimeFormat(t *testing.T) {
	stamp := UnixTime(1700000000)
	if got, want := stamp.String(), time.Unix(1700000000, 0).UTC().Format(time.RFC3339); got != want {
		t.Errorf("UnixTime.String() 为 %q，期望 %q", got, want)
	}
	if !UnixTime(0).IsZero() || UnixTime(0).String() != "" {
		t.Error("零值时间戳应当判定为空并输出空字符串")
	}
	if data, err := UnixTime(1700000000).MarshalJSON(); err != nil || string(data) != `"2023-11-14T22:13:20Z"` {
		t.Errorf("MarshalJSON 输出 %s（err=%v）", data, err)
	}
	if data, err := UnixTime(0).MarshalJSON(); err != nil || string(data) != "null" {
		t.Errorf("零值的 MarshalJSON 输出 %s（err=%v）", data, err)
	}
}
