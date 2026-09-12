package debrepo

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// maxControlLineSize 是单行的最大长度。索引里的 Description 等字段可能很长，
// 但没有实际意义上限，这里给一个足够宽松的限制以避免恶意数据撑爆内存。
const maxControlLineSize = 4 << 20

// Stanza 是 deb822 格式（Packages、Sources、Release、control 等）中的一段。
//
// 字段名不区分大小写；取值为"逻辑值"：续行（以空格或制表符开头的行）去掉一个前导
// 空白字符后，与上一行用 "\n" 连接，因此 Description、Files 等多行字段可以原样取出。
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

// Get 返回字段值，字段不存在时返回空字符串。name 不区分大小写。
func (s *Stanza) Get(name string) string {
	if s == nil {
		return ""
	}
	return s.values[strings.ToLower(name)]
}

// Has 判断字段是否存在（即使值为空）。
func (s *Stanza) Has(name string) bool {
	if s == nil {
		return false
	}
	_, ok := s.values[strings.ToLower(name)]
	return ok
}

// Names 按字段在文件中的出现顺序返回字段名。
func (s *Stanza) Names() []string {
	if s == nil {
		return nil
	}
	names := make([]string, len(s.names))
	copy(names, s.names)
	return names
}

// Len 返回字段数量。
func (s *Stanza) Len() int {
	if s == nil {
		return 0
	}
	return len(s.names)
}

// Fields 返回字段名（原样大小写）到值的映射副本。
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

// String 按文件中的顺序还原为 deb822 文本。
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

// set 写入字段值，同名（不区分大小写）字段会被覆盖并保留首次出现的位置。
func (s *Stanza) set(name, value string) {
	key := strings.ToLower(name)
	if _, ok := s.values[key]; !ok {
		s.names = append(s.names, strings.TrimSpace(name))
	}
	s.values[key] = value
}

// appendLine 追加一行续行内容。
func (s *Stanza) appendLine(name, line string) {
	key := strings.ToLower(name)
	s.values[key] += "\n" + line
}

// ParseStanzas 读取整份 deb822 数据并返回所有段落。
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

// ParseStanzasFunc 流式解析 deb822 数据，每读到一个段落就调用一次 fn。
// 段落之间以空行分隔，以 '#' 开头的行视为注释（与 apt 的行为一致）。
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
			// 空行表示一个段落结束；连续空行不会产生空段落。
			if err := flush(); err != nil {
				return err
			}
		case line[0] == '#':
			// apt 会忽略注释行，这里保持一致。
			continue
		case line[0] == ' ' || line[0] == '\t':
			if current == nil || lastField == "" {
				return fmt.Errorf("%w: 第 %d 行是续行，但前面没有字段: %q", ErrInvalidControl, lineNo, line)
			}
			current.appendLine(lastField, strings.TrimRight(line[1:], " \t"))
		default:
			colon := strings.IndexByte(line, ':')
			if colon <= 0 {
				return fmt.Errorf("%w: 第 %d 行缺少字段名: %q", ErrInvalidControl, lineNo, line)
			}
			name := strings.TrimSpace(line[:colon])
			if name == "" {
				return fmt.Errorf("%w: 第 %d 行的字段名为空: %q", ErrInvalidControl, lineNo, line)
			}
			if current == nil {
				current = &Stanza{values: make(map[string]string, 16)}
			}
			current.set(name, strings.TrimSpace(line[colon+1:]))
			lastField = name
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("debrepo: 读取 deb822 数据失败: %w", err)
	}
	return flush()
}
