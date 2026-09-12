package debrepo

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Version 是 Debian 版本号：[epoch:]upstream[-revision]。
//
//	1:1.22.1-9  →  Epoch=1, Upstream="1.22.1", Revision="9"
//	2.36        →  Epoch=0, Upstream="2.36",      Revision=""
type Version struct {
	// Epoch 是纪元，未显式给出时为 0。
	Epoch int `json:"epoch"`
	// Upstream 是上游版本号。
	Upstream string `json:"upstream"`
	// Revision 是 Debian 修订号（最后一个 '-' 之后的部分），可能为空。
	Revision string `json:"revision"`
}

// ParseVersion 解析 Debian 版本号。epoch 必须是数字，上游版本号不能为空。
func ParseVersion(raw string) (Version, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Version{}, fmt.Errorf("%w: 版本号为空", ErrInvalidVersion)
	}
	v := Version{}
	rest := raw
	if colon := strings.IndexByte(rest, ':'); colon >= 0 {
		epoch := rest[:colon]
		value, err := strconv.Atoi(strings.TrimSpace(epoch))
		if err != nil || value < 0 {
			return Version{}, fmt.Errorf("%w: 非法的 epoch %q（来自 %q）", ErrInvalidVersion, epoch, raw)
		}
		v.Epoch = value
		rest = rest[colon+1:]
	}
	if dash := strings.LastIndexByte(rest, '-'); dash >= 0 {
		v.Upstream = rest[:dash]
		v.Revision = rest[dash+1:]
	} else {
		v.Upstream = rest
	}
	if v.Upstream == "" {
		return Version{}, fmt.Errorf("%w: 缺少上游版本号（来自 %q）", ErrInvalidVersion, raw)
	}
	return v, nil
}

// MustParseVersion 与 ParseVersion 相同，但解析失败时 panic，便于在测试与常量中使用。
func MustParseVersion(raw string) Version {
	v, err := ParseVersion(raw)
	if err != nil {
		panic(err)
	}
	return v
}

// IsZero 判断版本号是否为空。
func (v Version) IsZero() bool { return v.Upstream == "" && v.Revision == "" && v.Epoch == 0 }

// String 返回 "epoch:upstream-revision"，epoch 为 0、revision 为空时省略对应部分。
func (v Version) String() string {
	var sb strings.Builder
	if v.Epoch != 0 {
		sb.WriteString(strconv.Itoa(v.Epoch))
		sb.WriteByte(':')
	}
	sb.WriteString(v.Upstream)
	if v.Revision != "" {
		sb.WriteByte('-')
		sb.WriteString(v.Revision)
	}
	return sb.String()
}

// Compare 按 Debian 规则比较版本：先 epoch，再上游版本，最后 Debian 修订号。
// 小于 0 表示 v 更旧，等于 0 表示相等，大于 0 表示 v 更新。
func (v Version) Compare(o Version) int {
	switch {
	case v.Epoch < o.Epoch:
		return -1
	case v.Epoch > o.Epoch:
		return 1
	}
	if c := compareVersionPart(v.Upstream, o.Upstream); c != 0 {
		return c
	}
	return compareVersionPart(v.Revision, o.Revision)
}

// MarshalJSON 输出为 Debian 风格版本字符串，空版本输出 null。
func (v Version) MarshalJSON() ([]byte, error) {
	if v.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(v.String())
}

// UnmarshalJSON 解析 Debian 风格版本字符串。
func (v *Version) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	parsed, err := ParseVersion(raw)
	if err != nil {
		return err
	}
	*v = parsed
	return nil
}

// MarshalText 实现 encoding.TextMarshaler。
func (v Version) MarshalText() ([]byte, error) { return []byte(v.String()), nil }

// UnmarshalText 实现 encoding.TextUnmarshaler。
func (v *Version) UnmarshalText(text []byte) error {
	parsed, err := ParseVersion(string(text))
	if err != nil {
		return err
	}
	*v = parsed
	return nil
}

// CompareVersions 用 Debian 版本比较规则（dpkg --compare-versions）比较两个版本字符串，
// 小于 0 表示 a 比 b 旧，等于 0 表示相等，大于 0 表示 a 比 b 新。
//
// 与 dpkg 保持一致的关键行为：
//
//   - 先比较 epoch（默认 0），例如 "1:1.0" > "2.0"；
//   - 版本被拆成"非数字段"与"数字段"交替比较，数字段按数值比较，非数字段按
//     字符顺序比较，字母排在所有非字母字符之前；
//   - "~" 排在所有内容（包括空字符串）之前，例如 "1.0~rc1" < "1.0"；
//   - 空修订号小于任何修订号，例如 "1.0" < "1.0-1"。
func CompareVersions(a, b string) int {
	va, errA := ParseVersion(a)
	vb, errB := ParseVersion(b)
	if errA != nil || errB != nil {
		// 版本号非法时退化为按字面比较，避免解析失败导致无法排序。
		return strings.Compare(a, b)
	}
	return va.Compare(vb)
}

// compareVersionPart 实现 dpkg 的 verrevcmp。
func compareVersionPart(a, b string) int {
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		// 非数字段：逐字符比较，遇到数字结束。
		for (i < len(a) && !isDigit(a[i])) || (j < len(b) && !isDigit(b[j])) {
			ac, bc := 0, 0
			if i < len(a) {
				ac = versionOrder(a[i])
			}
			if j < len(b) {
				bc = versionOrder(b[j])
			}
			if ac != bc {
				return sign(ac - bc)
			}
			i++
			j++
		}

		// 数字段：先跳过前导 0，再按数值（位数 + 逐位差）比较。
		for i < len(a) && a[i] == '0' {
			i++
		}
		for j < len(b) && b[j] == '0' {
			j++
		}
		firstDiff := 0
		for i < len(a) && j < len(b) && isDigit(a[i]) && isDigit(b[j]) {
			if firstDiff == 0 {
				firstDiff = int(a[i]) - int(b[j])
			}
			i++
			j++
		}
		if i < len(a) && isDigit(a[i]) {
			return 1
		}
		if j < len(b) && isDigit(b[j]) {
			return -1
		}
		if firstDiff != 0 {
			return sign(firstDiff)
		}
	}
	return 0
}

// versionOrder 返回字符在比较时的权重，与 dpkg 的 order() 一致。
func versionOrder(c byte) int {
	switch {
	case isDigit(c):
		return 0
	case isAlpha(c):
		return int(c)
	case c == '~':
		return -1
	default:
		return int(c) + 256
	}
}

func sign(v int) int {
	switch {
	case v < 0:
		return -1
	case v > 0:
		return 1
	default:
		return 0
	}
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isAlpha(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isAlnum(c byte) bool { return isDigit(c) || isAlpha(c) }
