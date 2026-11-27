package desktop

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/egoist/mygo/ui/editor"
)

type seenDomain struct {
	line      int
	ip        string
	diagnosed bool
}

func HostsLanguage() *editor.Language {
	return &editor.Language{
		Name:        "Hosts",
		Extensions:  []string{".hosts"},
		LineComment: "#",
		Tokenizer: editor.TokenizerFunc(func(line string, state uint64, tokens []editor.Token) ([]editor.Token, uint64) {
			comment := strings.IndexByte(line, '#')
			if comment == 0 {
				return append(tokens, editor.Token{End: len(line), Kind: editor.TokenComment}), 0
			}
			rule := line
			if comment >= 0 {
				rule = line[:comment]
			}
			offset := 0
			fieldIndex := 0
			for _, field := range strings.Fields(rule) {
				at := strings.Index(rule[offset:], field) + offset
				if at > offset {
					tokens = append(tokens, editor.Token{End: at, Kind: editor.TokenText})
				}
				kind := editor.TokenString
				if fieldIndex == 0 {
					kind = editor.TokenKeyword
				}
				tokens = append(tokens, editor.Token{End: at + len(field), Kind: kind})
				offset = at + len(field)
				fieldIndex++
			}
			if comment >= 0 {
				if comment > len(rule) {
					tokens = append(tokens, editor.Token{End: comment, Kind: editor.TokenText})
				}
				tokens = append(tokens, editor.Token{End: len(line), Kind: editor.TokenComment})
			}
			return tokens, 0
		}),
	}
}

func HostsDiagnostics(buffer *editor.Buffer) []editor.Diagnostic {
	var diagnostics []editor.Diagnostic
	seen := make(map[string]*seenDomain)
	for line := range buffer.LineCount() {
		text := buffer.Line(line)
		content := text
		if comment := strings.IndexByte(content, '#'); comment >= 0 {
			content = content[:comment]
		}
		fields := strings.Fields(content)
		if len(fields) == 0 {
			continue
		}
		if _, err := netip.ParseAddr(fields[0]); err != nil {
			diagnostics = append(diagnostics, hostsDiagnostic(buffer, line, editor.SeverityError, "invalid IP address"))
			continue
		}
		for _, domain := range fields[1:] {
			previous, exists := seen[domain]
			if !exists {
				seen[domain] = &seenDomain{line: line, ip: fields[0]}
				continue
			}
			if !previous.diagnosed {
				severity := editor.SeverityWarning
				if previous.ip != fields[0] {
					severity = editor.SeverityError
				}
				diagnostics = append(diagnostics, hostsDiagnostic(buffer, previous.line, severity, fmt.Sprintf("duplicate domain %q", domain)))
				previous.diagnosed = true
			}
			kind := "same IP"
			severity := editor.SeverityWarning
			if previous.ip != fields[0] {
				kind = "different IP"
				severity = editor.SeverityError
			}
			diagnostics = append(diagnostics, hostsDiagnostic(buffer, line, severity, fmt.Sprintf("duplicate domain %q (%s)", domain, kind)))
		}
	}
	return diagnostics
}

func hostsDiagnostic(buffer *editor.Buffer, line int, severity editor.Severity, message string) editor.Diagnostic {
	return editor.Diagnostic{
		Start:    buffer.Offset(editor.Pos{Line: line}),
		End:      buffer.Offset(editor.Pos{Line: line}) + len(buffer.Line(line)),
		Severity: severity,
		Message:  message,
		Source:   "hosts",
	}
}
