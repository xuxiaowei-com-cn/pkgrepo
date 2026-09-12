package rpmrepo

import (
	"strconv"
	"strings"
)

// CompareVersions compares two version strings using the RPM version comparison rules (rpmvercmp):
// a result less than 0 means a is older than b, 0 means they are equal, and a result greater than 0
// means a is newer than b.
//
// The key behaviors that keep it consistent with rpm/dnf:
//
//   - Numeric segments are compared by value and alphabetic segments lexicographically; a numeric
//     segment is always newer than an alphabetic one ("1.1" > "1.a").
//   - Separators do not participate in the comparison, so "1.0", "1-0", and "1_0" are equal.
//   - "~" sorts before everything else (pre-release), so "1.0~rc1" < "1.0".
//   - "^" sorts after the empty string and before other characters, so
//     "1.0" < "1.0^" < "1.0.1".
func CompareVersions(a, b string) int {
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		// Skip separators; "~" and "^" carry comparison semantics and must not be skipped.
		for i < len(a) && !isAlnum(a[i]) && a[i] != '~' && a[i] != '^' {
			i++
		}
		for j < len(b) && !isAlnum(b[j]) && b[j] != '~' && b[j] != '^' {
			j++
		}

		// "~" sorts before everything, including the empty string.
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

		// "^" sorts after the empty string and before other characters.
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

		// One side is exhausted, so the comparison is over.
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

		// The two segments differ in type (one side is empty): a numeric segment is always newer.
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

// compareSegment compares two segments of the same type (both numeric or both alphabetic).
func compareSegment(a, b string, numeric bool) int {
	if numeric {
		// After trimming leading zeros, the longer value is greater; when the lengths are equal,
		// compare lexicographically.
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

// EVR holds the RPM version information: Epoch, Version, and Release.
type EVR struct {
	Epoch   string `xml:"epoch,attr" json:"epoch"`
	Version string `xml:"ver,attr" json:"version"`
	Release string `xml:"rel,attr" json:"release"`
}

// EpochInt returns the numeric value of Epoch, or 0 when Epoch is empty or invalid.
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

// String returns "epoch:version-release", omitting the epoch when it is 0 and the release when it is
// empty.
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

// Compare compares versions by the RPM rules: Epoch first, then Version, and finally Release.
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
