package debrepo

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// maxControlLineSize is the maximum length of a single line. Fields such as Description in an index
// can be very long and have no practical upper bound, so this limit is deliberately generous to keep
// malicious data from exhausting memory.
const maxControlLineSize = 4 << 20

// Stanza is one paragraph in the deb822 format (Packages, Sources, Release, control, and so on).
//
// Field names are case-insensitive. A value is a "logical value": continuation lines (lines starting
// with a space or a tab) have one leading whitespace character removed and are joined to the previous
// line with "\n", so multi-line fields such as Description and Files are returned as they are.
//
//	Package: nginx
//	Depends: libc6 (>= 2.34),
//	 libssl3
//	Description: small, powerful, scalable web/proxy server
//	 .
//	 Nginx is a web server and a reverse proxy.
type Stanza struct {
	names  []string
	values map[string]string
}

// Get returns the field value, or an empty string when the field is absent. name is
// case-insensitive.
func (s *Stanza) Get(name string) string {
	if s == nil {
		return ""
	}
	return s.values[strings.ToLower(name)]
}

// Has reports whether the field exists (even when its value is empty).
func (s *Stanza) Has(name string) bool {
	if s == nil {
		return false
	}
	_, ok := s.values[strings.ToLower(name)]
	return ok
}

// Names returns the field names in the order they appear in the file.
func (s *Stanza) Names() []string {
	if s == nil {
		return nil
	}
	names := make([]string, len(s.names))
	copy(names, s.names)
	return names
}

// Len returns the number of fields.
func (s *Stanza) Len() int {
	if s == nil {
		return 0
	}
	return len(s.names)
}

// Fields returns a copy of the field name (with its original case) to value mapping.
func (s *Stanza) Fields() map[string]string {
	if s == nil {
		return nil
	}
	fields := make(map[string]string, len(s.names))
	for _, name := range s.names {
		fields[name] = s.values[strings.ToLower(name)]
	}
	return fields
}

// String renders the stanza back into deb822 text in file order.
func (s *Stanza) String() string {
	if s == nil {
		return ""
	}
	var sb strings.Builder
	for _, name := range s.names {
		value := s.values[strings.ToLower(name)]
		lines := strings.Split(value, "\n")
		sb.WriteString(name)
		sb.WriteString(":")
		if lines[0] != "" {
			sb.WriteString(" ")
			sb.WriteString(lines[0])
		}
		sb.WriteByte('\n')
		for _, line := range lines[1:] {
			sb.WriteByte(' ')
			sb.WriteString(line)
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// set writes a field value; a field with the same name (case-insensitively) is overwritten and keeps
// the position of its first occurrence.
func (s *Stanza) set(name, value string) {
	key := strings.ToLower(name)
	if _, ok := s.values[key]; !ok {
		s.names = append(s.names, strings.TrimSpace(name))
	}
	s.values[key] = value
}

// appendLine appends one continuation line.
func (s *Stanza) appendLine(name, line string) {
	key := strings.ToLower(name)
	s.values[key] += "\n" + line
}

// ParseStanzas reads a complete deb822 document and returns all paragraphs.
func ParseStanzas(r io.Reader) ([]Stanza, error) {
	var stanzas []Stanza
	err := ParseStanzasFunc(r, func(stanza *Stanza) error {
		stanzas = append(stanzas, *stanza)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return stanzas, nil
}

// ParseStanzasFunc streams over deb822 data, calling fn once per paragraph.
// Paragraphs are separated by blank lines, and lines starting with '#' are treated as comments (which
// matches apt's behavior).
func ParseStanzasFunc(r io.Reader, fn func(*Stanza) error) error {
	if fn == nil {
		return nil
	}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxControlLineSize)

	var current *Stanza
	lastField := ""
	lineNo := 0

	flush := func() error {
		if current == nil {
			return nil
		}
		stanza := current
		current, lastField = nil, ""
		return fn(stanza)
	}

	for scanner.Scan() {
		lineNo++
		line := strings.TrimRight(scanner.Text(), "\r")
		switch {
		case strings.TrimSpace(line) == "":
			// A blank line ends a paragraph; consecutive blank lines do not produce empty
			// paragraphs.
			if err := flush(); err != nil {
				return err
			}
		case line[0] == '#':
			// apt ignores comment lines, and this behaves the same way.
			continue
		case line[0] == ' ' || line[0] == '\t':
			if current == nil || lastField == "" {
				return fmt.Errorf("%w: line %d is a continuation line but there is no preceding field: %q", ErrInvalidControl, lineNo, line)
			}
			current.appendLine(lastField, strings.TrimRight(line[1:], " \t"))
		default:
			colon := strings.IndexByte(line, ':')
			if colon <= 0 {
				return fmt.Errorf("%w: line %d is missing a field name: %q", ErrInvalidControl, lineNo, line)
			}
			name := strings.TrimSpace(line[:colon])
			if name == "" {
				return fmt.Errorf("%w: the field name on line %d is empty: %q", ErrInvalidControl, lineNo, line)
			}
			if current == nil {
				current = &Stanza{values: make(map[string]string, 16)}
			}
			current.set(name, strings.TrimSpace(line[colon+1:]))
			lastField = name
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("debrepo: reading deb822 data failed: %w", err)
	}
	return flush()
}
