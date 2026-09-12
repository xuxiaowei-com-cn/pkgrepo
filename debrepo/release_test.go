package debrepo

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const releaseSample = `Origin: Debian
Label: Debian
Suite: stable
Version: 13.6
Codename: trixie
Date: Sat, 11 Jul 2026 09:02:23 UTC
Valid-Until: Sat, 18 Jul 2026 09:02:23 UTC
Acquire-By-Hash: yes
Architectures: all amd64 arm64
Components: main contrib
Description: Debian 13.6 Released 11 July 2026
SHA256:
 3ab4e811cf4f3e5a335d382c58cc19d85f1abe7a4ef4689160ca1f637fa0e9b3 9672648 main/binary-amd64/Packages.xz
 68343c85daeefd3dc3ddb5a87fd54b6f 4531124 main/binary-all/Packages.xz
MD5Sum:
 99af1dc3e3638eeafb9cda53adc08cc9  9672648 main/binary-amd64/Packages.xz
`

func TestParseRelease(t *testing.T) {
	release, err := ParseRelease(strings.NewReader(releaseSample))
	if err != nil {
		t.Fatalf("ParseRelease failed: %v", err)
	}
	if release.Origin != "Debian" || release.Label != "Debian" {
		t.Errorf("Origin/Label = %q/%q", release.Origin, release.Label)
	}
	if release.Suite != "stable" || release.Codename != "trixie" || release.Version != "13.6" {
		t.Errorf("Suite/Codename/Version = %q/%q/%q", release.Suite, release.Codename, release.Version)
	}
	if !release.AcquireByHash {
		t.Error("AcquireByHash should be true")
	}
	if strings.Join(release.Architectures, " ") != "all amd64 arm64" {
		t.Errorf("Architectures = %v", release.Architectures)
	}
	if strings.Join(release.Components, " ") != "main contrib" {
		t.Errorf("Components = %v", release.Components)
	}
	if got := release.Date.Format("2006-01-02 15:04:05"); got != "2026-07-11 09:02:23" {
		t.Errorf("Date = %q", got)
	}
	if release.Date.IsZero() || !release.Date.IsSet() {
		t.Errorf("unexpected Date parse result: %+v", release.Date)
	}
	if release.Expired(release.Date.Time()) {
		t.Error("the repository should not be expired before Valid-Until")
	}
	if !release.Expired(release.Date.Time().Add(8 * 24 * time.Hour)) {
		t.Error("the repository should be expired after Valid-Until")
	}
}

func TestReleaseLookup(t *testing.T) {
	release, err := ParseRelease(strings.NewReader(releaseSample))
	if err != nil {
		t.Fatalf("ParseRelease failed: %v", err)
	}
	entry, ok := release.Lookup("main/binary-amd64/Packages.xz")
	if !ok {
		t.Fatal("main/binary-amd64/Packages.xz should be found")
	}
	// When the same path has both MD5 and SHA256, the stronger SHA256 should be returned.
	if entry.Checksum.Type != "sha256" {
		t.Errorf("the algorithm returned by Lookup = %q, want sha256", entry.Checksum.Type)
	}
	if entry.Size != 9672648 {
		t.Errorf("the size returned by Lookup = %d", entry.Size)
	}
	if md5Entry, ok := release.LookupAlgorithm("main/binary-amd64/Packages.xz", "MD5Sum"); !ok ||
		md5Entry.Checksum.Type != "md5" {
		t.Errorf("LookupAlgorithm(md5) = %+v, %t", md5Entry, ok)
	}
	if _, ok := release.Lookup("main/binary-i386/Packages.xz"); ok {
		t.Error("a nonexistent path should not be found")
	}
	if !release.Has("main/binary-all/Packages.xz") || release.Has("main/binary-arm64/Packages.xz") {
		t.Error("Has returned a wrong result")
	}
	if got := len(release.Files()); got != 3 {
		t.Errorf("len(Files()) = %d, want 3 (two SHA256 entries plus one MD5 entry)", got)
	}
	byStrength := release.FilesByStrength()
	if len(byStrength) != 2 {
		t.Fatalf("len(FilesByStrength()) = %d, want 2 (deduplicated by path)", len(byStrength))
	}
	if byStrength[0].Path != "main/binary-amd64/Packages.xz" || byStrength[0].Checksum.Type != "sha256" {
		t.Errorf("FilesByStrength() = %+v", byStrength[0])
	}
	if byStrength[1].Path != "main/binary-all/Packages.xz" || byStrength[1].Checksum.Type != "sha256" {
		t.Errorf("FilesByStrength() = %+v", byStrength[1])
	}
	if got := release.Field("Codename"); got != "trixie" {
		t.Errorf("Field(Codename) = %q", got)
	}
	if fields := release.FieldsMap(); fields["release"] != "" || fields["codename"] != "trixie" {
		t.Errorf("FieldsMap = %v", fields)
	}
}

func TestReleaseJSON(t *testing.T) {
	release, err := ParseRelease(strings.NewReader(releaseSample))
	if err != nil {
		t.Fatalf("ParseRelease failed: %v", err)
	}
	data, err := json.Marshal(release)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	var decoded struct {
		Suite         string            `json:"suite"`
		Codename      string            `json:"codename"`
		AcquireByHash bool              `json:"acquire_by_hash"`
		Fields        map[string]string `json:"fields"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if decoded.Suite != "stable" || decoded.Codename != "trixie" || !decoded.AcquireByHash {
		t.Errorf("JSON = %s", data)
	}
	if decoded.Fields["origin"] != "Debian" {
		t.Errorf("the JSON should contain the raw fields: %s", data)
	}
}

func TestParseInRelease(t *testing.T) {
	const inRelease = `-----BEGIN PGP SIGNED MESSAGE-----
Hash: SHA256

Origin: Ubuntu
Label: Ubuntu
Suite: noble
Codename: noble
Architectures: amd64
Components: main
SHA256:
 262e2d34f95ba40bbff222c1902a97de          7165069 main/binary-amd64/Packages
-----BEGIN PGP SIGNATURE-----

iQIzBAEBCgAdFiEE...
=vUFR
-----END PGP SIGNATURE-----
`
	release, err := ParseRelease(strings.NewReader(inRelease))
	if err != nil {
		t.Fatalf("ParseRelease failed: %v", err)
	}
	if release.Origin != "Ubuntu" || release.Suite != "noble" {
		t.Errorf("parse result = %+v", release)
	}
	entry, ok := release.Lookup("main/binary-amd64/Packages")
	if !ok || entry.Size != 7165069 {
		t.Errorf("Lookup = %+v, %t", entry, ok)
	}
}

func TestClearPGPArmor(t *testing.T) {
	plain := []byte(releaseSample)
	if got := ClearPGPArmor(plain); string(got) != string(plain) {
		t.Error("unsigned data should be returned unchanged")
	}
	escaped := []byte("-----BEGIN PGP SIGNED MESSAGE-----\nHash: SHA256\n\n- -----BEGIN escaped body\nOrigin: Debian\n-----BEGIN PGP SIGNATURE-----\nx\n-----END PGP SIGNATURE-----\n")
	got := string(ClearPGPArmor(escaped))
	if !strings.Contains(got, "-----BEGIN escaped body") {
		t.Errorf("the dash escape was not restored: %q", got)
	}
	if strings.Contains(got, "PGP SIGNATURE") || strings.Contains(got, "Hash: SHA256") {
		t.Errorf("the signature and headers were not stripped: %q", got)
	}
}

func TestParseReleaseErrors(t *testing.T) {
	if _, err := ParseRelease(strings.NewReader("")); err == nil {
		t.Error("an empty Release file should return an error")
	}
	if _, err := ParseRelease(strings.NewReader("SHA256:\n abc 123\n")); err == nil {
		t.Error("an invalid checksum section should return an error")
	}
	if _, err := ParseRelease(strings.NewReader("Date: yesterday\n")); err == nil {
		t.Error("an invalid time should return an error")
	}
}

func TestTimeJSON(t *testing.T) {
	parsed, err := ParseTime("Sat, 11 Jul 2026 09:02:23 UTC")
	if err != nil {
		t.Fatalf("ParseTime failed: %v", err)
	}
	data, err := json.Marshal(parsed)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if string(data) != `"2026-07-11T09:02:23Z"` {
		t.Errorf("Marshal = %s", data)
	}
	var decoded Time
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if !decoded.Time().Equal(parsed.Time()) {
		t.Errorf("Unmarshal = %v, want %v", decoded.Time(), parsed.Time())
	}
	var zero Time
	data, err = json.Marshal(zero)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if string(data) != "null" {
		t.Errorf("Marshal of a zero time = %s, want null", data)
	}
	if empty, err := ParseTime(""); err != nil || !empty.IsZero() {
		t.Errorf("ParseTime(\"\") = %+v, %v", empty, err)
	}
}
