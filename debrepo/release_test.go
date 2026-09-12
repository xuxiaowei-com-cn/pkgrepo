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
		t.Fatalf("ParseRelease 失败: %v", err)
	}
	if release.Origin != "Debian" || release.Label != "Debian" {
		t.Errorf("Origin/Label = %q/%q", release.Origin, release.Label)
	}
	if release.Suite != "stable" || release.Codename != "trixie" || release.Version != "13.6" {
		t.Errorf("Suite/Codename/Version = %q/%q/%q", release.Suite, release.Codename, release.Version)
	}
	if !release.AcquireByHash {
		t.Error("AcquireByHash 应当为 true")
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
		t.Errorf("Date 解析异常: %+v", release.Date)
	}
	if release.Expired(release.Date.Time()) {
		t.Error("在 Valid-Until 之前不应判定为过期")
	}
	if !release.Expired(release.Date.Time().Add(8 * 24 * time.Hour)) {
		t.Error("超过 Valid-Until 后应判定为过期")
	}
}

func TestReleaseLookup(t *testing.T) {
	release, err := ParseRelease(strings.NewReader(releaseSample))
	if err != nil {
		t.Fatalf("ParseRelease 失败: %v", err)
	}
	entry, ok := release.Lookup("main/binary-amd64/Packages.xz")
	if !ok {
		t.Fatal("应当能找到 main/binary-amd64/Packages.xz")
	}
	// 同一路径同时有 MD5 与 SHA256 时应当返回更强的 SHA256。
	if entry.Checksum.Type != "sha256" {
		t.Errorf("Lookup 返回的算法 = %q，期望 sha256", entry.Checksum.Type)
	}
	if entry.Size != 9672648 {
		t.Errorf("Lookup 返回的大小 = %d", entry.Size)
	}
	if md5Entry, ok := release.LookupAlgorithm("main/binary-amd64/Packages.xz", "MD5Sum"); !ok ||
		md5Entry.Checksum.Type != "md5" {
		t.Errorf("LookupAlgorithm(md5) = %+v, %t", md5Entry, ok)
	}
	if _, ok := release.Lookup("main/binary-i386/Packages.xz"); ok {
		t.Error("不存在的路径不应被找到")
	}
	if !release.Has("main/binary-all/Packages.xz") || release.Has("main/binary-arm64/Packages.xz") {
		t.Error("Has 判断错误")
	}
	if got := len(release.Files()); got != 3 {
		t.Errorf("Files() 长度 = %d，期望 3（SHA256 两条 + MD5 一条）", got)
	}
	byStrength := release.FilesByStrength()
	if len(byStrength) != 2 {
		t.Fatalf("FilesByStrength() 长度 = %d，期望 2（按路径去重）", len(byStrength))
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
		t.Fatalf("ParseRelease 失败: %v", err)
	}
	data, err := json.Marshal(release)
	if err != nil {
		t.Fatalf("Marshal 失败: %v", err)
	}
	var decoded struct {
		Suite         string            `json:"suite"`
		Codename      string            `json:"codename"`
		AcquireByHash bool              `json:"acquire_by_hash"`
		Fields        map[string]string `json:"fields"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal 失败: %v", err)
	}
	if decoded.Suite != "stable" || decoded.Codename != "trixie" || !decoded.AcquireByHash {
		t.Errorf("JSON = %s", data)
	}
	if decoded.Fields["origin"] != "Debian" {
		t.Errorf("JSON 中应当包含原始字段: %s", data)
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
		t.Fatalf("ParseRelease 失败: %v", err)
	}
	if release.Origin != "Ubuntu" || release.Suite != "noble" {
		t.Errorf("解析结果 = %+v", release)
	}
	entry, ok := release.Lookup("main/binary-amd64/Packages")
	if !ok || entry.Size != 7165069 {
		t.Errorf("Lookup = %+v, %t", entry, ok)
	}
}

func TestClearPGPArmor(t *testing.T) {
	plain := []byte(releaseSample)
	if got := ClearPGPArmor(plain); string(got) != string(plain) {
		t.Error("非签名数据应当原样返回")
	}
	escaped := []byte("-----BEGIN PGP SIGNED MESSAGE-----\nHash: SHA256\n\n- -----BEGIN 被转义的正文\nOrigin: Debian\n-----BEGIN PGP SIGNATURE-----\nx\n-----END PGP SIGNATURE-----\n")
	got := string(ClearPGPArmor(escaped))
	if !strings.Contains(got, "-----BEGIN 被转义的正文") {
		t.Errorf("dash-escape 未被还原: %q", got)
	}
	if strings.Contains(got, "PGP SIGNATURE") || strings.Contains(got, "Hash: SHA256") {
		t.Errorf("签名与头部未被剥离: %q", got)
	}
}

func TestParseReleaseErrors(t *testing.T) {
	if _, err := ParseRelease(strings.NewReader("")); err == nil {
		t.Error("空 Release 应当返回错误")
	}
	if _, err := ParseRelease(strings.NewReader("SHA256:\n abc 123\n")); err == nil {
		t.Error("非法的校验值分节应当返回错误")
	}
	if _, err := ParseRelease(strings.NewReader("Date: 昨天\n")); err == nil {
		t.Error("非法时间应当返回错误")
	}
}

func TestTimeJSON(t *testing.T) {
	parsed, err := ParseTime("Sat, 11 Jul 2026 09:02:23 UTC")
	if err != nil {
		t.Fatalf("ParseTime 失败: %v", err)
	}
	data, err := json.Marshal(parsed)
	if err != nil {
		t.Fatalf("Marshal 失败: %v", err)
	}
	if string(data) != `"2026-07-11T09:02:23Z"` {
		t.Errorf("Marshal = %s", data)
	}
	var decoded Time
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal 失败: %v", err)
	}
	if !decoded.Time().Equal(parsed.Time()) {
		t.Errorf("Unmarshal = %v，期望 %v", decoded.Time(), parsed.Time())
	}
	var zero Time
	data, err = json.Marshal(zero)
	if err != nil {
		t.Fatalf("Marshal 失败: %v", err)
	}
	if string(data) != "null" {
		t.Errorf("零值时间 Marshal = %s，期望 null", data)
	}
	if empty, err := ParseTime(""); err != nil || !empty.IsZero() {
		t.Errorf("ParseTime(\"\") = %+v, %v", empty, err)
	}
}
