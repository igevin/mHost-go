package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui/editor"
	"github.com/igevin/mHost-go/internal/hostsfile"
)

// HostsLanguage 是编辑器语法高亮（保留 mygo 组件内的 tokenizer，
// 高亮规则与解析语义无关）。
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

// HostsDiagnostics 复用 hostsfile.Parse（与真实服务、差分测试同一
// 语义来源），把解析错误与重复域名映射为编辑器诊断：
// 解析错误为 error 级；同 IP 重复 warning 级、异 IP 重复 error 级。
func HostsDiagnostics(buffer *editor.Buffer) []editor.Diagnostic {
	parsed := hostsfile.Parse(buffer.String())
	var diagnostics []editor.Diagnostic
	for _, err := range parsed.Errors {
		line := err.Line - 1
		if line < 0 || line >= buffer.LineCount() {
			continue
		}
		message := "invalid IP address"
		switch err.Kind {
		case hostsfile.ErrMalformedLine:
			message = "malformed line — expected: IP followed by one or more domains"
		case hostsfile.ErrInvalidDomain:
			message = fmt.Sprintf("invalid domain %q", err.Token)
		}
		diagnostics = append(diagnostics, hostsDiagnostic(buffer, line, editor.SeverityError, message))
	}
	for _, duplicate := range parsed.Duplicates {
		severity := editor.SeverityWarning
		kind := " (same IP)"
		if duplicate.Kind == hostsfile.DuplicateDifferentIP {
			severity = editor.SeverityError
			kind = " (different IP)"
		}
		for i, lineNumber := range duplicate.Lines {
			line := lineNumber - 1
			if line < 0 || line >= buffer.LineCount() {
				continue
			}
			message := fmt.Sprintf("duplicate domain %q", duplicate.Domain)
			if i > 0 {
				message += kind
			}
			diagnostics = append(diagnostics, hostsDiagnostic(buffer, line, severity, message))
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
