package rpmrepo

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// Parsing of a real repomd.xml is verified in realrepo_test.go; this file covers the field details
// and error branches.

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
		t.Fatalf("ParseRepoMD failed: %v", err)
	}
	if repomd.Revision != "1700000000" {
		t.Errorf("revision is %q", repomd.Revision)
	}
	if got := strings.Join(repomd.Types(), ","); got != "primary,primary_db" {
		t.Errorf("metadata types are %q", got)
	}

	primary, err := repomd.Primary()
	if err != nil {
		t.Fatalf("Primary failed: %v", err)
	}
	if primary.Checksum.Value != "aaa" || primary.OpenChecksum.Value != "bbb" {
		t.Errorf("unexpected primary checksums: %+v", primary)
	}
	if primary.Size != 42531 || primary.OpenSize != 883960 {
		t.Errorf("unexpected primary sizes: size=%d open-size=%d", primary.Size, primary.OpenSize)
	}
	if got, want := primary.Timestamp.Time(), time.Unix(1700000000, 0).UTC(); !got.Equal(want) {
		t.Errorf("primary time is %v, want %v", got, want)
	}

	// Type names are case-insensitive.
	primaryDB, ok := repomd.DataByType("PRIMARY_DB")
	if !ok {
		t.Fatal("primary_db metadata was not found")
	}
	if primaryDB.DatabaseVersion != "10" {
		t.Errorf("database_version is %q, want 10", primaryDB.DatabaseVersion)
	}
	if primaryDB.OpenChecksum.Value != "" {
		t.Errorf("primary_db should not have an open-checksum: %+v", primaryDB.OpenChecksum)
	}
	if got, want := primaryDB.Timestamp.Time(), time.Unix(1700000100, 0).UTC(); !got.Equal(want) {
		t.Errorf("primary_db time is %v, want %v", got, want)
	}
	if primaryDB.Timestamp.IsZero() {
		t.Error("the timestamp should not be 0")
	}

	checksum := primary.Checksum
	if checksum.String() != "sha256:aaa" {
		t.Errorf("Checksum.String() is %q", checksum.String())
	}
}

func TestRepoMDPrimaryMissing(t *testing.T) {
	const document = `<?xml version="1.0" encoding="UTF-8"?>
<repomd><data type="other"><location href="repodata/other.xml.gz"/></data></repomd>`
	repomd, err := ParseRepoMD(strings.NewReader(document))
	if err != nil {
		t.Fatalf("ParseRepoMD failed: %v", err)
	}
	if _, err := repomd.Primary(); !errors.Is(err, ErrPrimaryNotFound) {
		t.Errorf("error is %v, want ErrPrimaryNotFound", err)
	}
	if _, ok := repomd.DataByType(DataTypePrimary); ok {
		t.Error("primary metadata should not be found")
	}
}

func TestUnixTimeErrors(t *testing.T) {
	const document = `<?xml version="1.0" encoding="UTF-8"?>
<repomd><data type="primary"><timestamp>not a time</timestamp>
<location href="repodata/primary.xml.gz"/></data></repomd>`
	if _, err := ParseRepoMD(strings.NewReader(document)); err == nil {
		t.Fatal("an invalid timestamp should fail")
	}
}

func TestUnixTimeFormat(t *testing.T) {
	stamp := UnixTime(1700000000)
	if got, want := stamp.String(), time.Unix(1700000000, 0).UTC().Format(time.RFC3339); got != want {
		t.Errorf("UnixTime.String() is %q, want %q", got, want)
	}
	if !UnixTime(0).IsZero() || UnixTime(0).String() != "" {
		t.Error("a zero timestamp should be reported as empty and print an empty string")
	}
	if data, err := UnixTime(1700000000).MarshalJSON(); err != nil || string(data) != `"2023-11-14T22:13:20Z"` {
		t.Errorf("MarshalJSON output %s (err=%v)", data, err)
	}
	if data, err := UnixTime(0).MarshalJSON(); err != nil || string(data) != "null" {
		t.Errorf("MarshalJSON output of a zero value is %s (err=%v)", data, err)
	}
}
