package rpmrepo

import (
	"strconv"
	"strings"
)

// CompareVersions 使用 RPM 的版本比较规则（rpmvercmp）比较两个版本字符串，
// 小于 0 表示 a 小于 b，等于 0 表示相等，大于 0 表示 a 大于 b。
//
// 与 rpm/dnf 保持一致的关键行为：
//
//   - 数字段按数值比较，字母段按字典序比较，数字段总是比字母段新（"1.1" > "1.a"）；
//   - 分隔符不参与比较，"1.0"、"1-0"、"1_0" 视为相等；
//   - "~" 排在所有内容之前（预发布），"1.0~rc1" < "1.0"；
//   - "^" 排在空字符串之后、其他字符之前，"1.0" < "1.0^" < "1.0.1"。
func CompareVersions(a, b string) int {
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		// 跳过分隔符；"~" 与 "^" 具有比较语义，不能跳过。
		for i < len(a) && !isAlnum(a[i]) && a[i] != '~' && a[i] != '^' {
			i++
		}
		for j < len(b) && !isAlnum(b[j]) && b[j] != '~' && b[j] != '^' {
			j++
		}

		// "~" 排在所有内容（包括空字符串）之前。
		if (i < len(a) && a[i] == '~') || (j < len(b) && b[j] == '~') {
			switch {
			case i >= len(a) || a[i] != '~':
				return 1
			case j >= len(b) || b[j] != '~':
				return -1
			}
			i++
			j++
			continue
		}

		// "^" 排在空字符串之后、其他字符之前。
		if (i < len(a) && a[i] == '^') || (j < len(b) && b[j] == '^') {
			switch {
			case i >= len(a):
				return -1
			case j >= len(b):
				return 1
			case a[i] != '^':
				return 1
			case b[j] != '^':
				return -1
			}
			i++
			j++
			continue
		}

		// 任一侧已结束，比较结束。
		if i >= len(a) || j >= len(b) {
			break
		}

		startA, startB := i, j
		numeric := isDigit(a[i])
		if numeric {
			for i < len(a) && isDigit(a[i]) {
				i++
			}
			for j < len(b) && isDigit(b[j]) {
				j++
			}
		} else {
			for i < len(a) && isAlpha(a[i]) {
				i++
			}
			for j < len(b) && isAlpha(b[j]) {
				j++
			}
		}

		// 两侧段类型不同（一侧为空）：数字段总是更新。
		if j == startB {
			if numeric {
				return 1
			}
			return -1
		}

		if c := compareSegment(a[startA:i], b[startB:j], numeric); c != 0 {
			return c
		}
	}

	switch {
	case i >= len(a) && j >= len(b):
		return 0
	case i >= len(a):
		return -1
	default:
		return 1
	}
}

// compareSegment 比较两个同类型（同为数字或同为字母）的段。
func compareSegment(a, b string, numeric bool) int {
	if numeric {
		// 去掉前导 0 后，位数多的更大；位数相同再按字典序比较。
		a = strings.TrimLeft(a, "0")
		b = strings.TrimLeft(b, "0")
		if len(a) != len(b) {
			if len(a) > len(b) {
				return 1
			}
			return -1
		}
	}
	return strings.Compare(a, b)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isAlpha(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isAlnum(c byte) bool { return isDigit(c) || isAlpha(c) }

// EVR 是 RPM 的版本信息：Epoch（纪元）、Version（版本）、Release（发布号）。
type EVR struct {
	Epoch   string `xml:"epoch,attr" json:"epoch"`
	Version string `xml:"ver,attr" json:"version"`
	Release string `xml:"rel,attr" json:"release"`
}

// EpochInt 返回 Epoch 的数值，Epoch 为空或非法时返回 0。
func (e EVR) EpochInt() int {
	if e.Epoch == "" {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(e.Epoch))
	if err != nil {
		return 0
	}
	return n
}

// String 返回 "epoch:version-release"，Epoch 为 0、Release 为空时省略对应部分。
func (e EVR) String() string {
	var sb strings.Builder
	if e.Epoch != "" && e.EpochInt() != 0 {
		sb.WriteString(e.Epoch)
		sb.WriteByte(':')
	}
	sb.WriteString(e.Version)
	if e.Release != "" {
		sb.WriteByte('-')
		sb.WriteString(e.Release)
	}
	return sb.String()
}

// Compare 按 RPM 规则比较版本：先比较 Epoch，再比较 Version，最后比较 Release。
func (e EVR) Compare(o EVR) int {
	if c := compareEpoch(e.EpochInt(), o.EpochInt()); c != 0 {
		return c
	}
	if c := CompareVersions(e.Version, o.Version); c != 0 {
		return c
	}
	return CompareVersions(e.Release, o.Release)
}

func compareEpoch(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
