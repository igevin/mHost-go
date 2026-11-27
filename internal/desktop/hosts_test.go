package desktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui/editor"
)

func TestHostsDiagnostics_InvalidIPAndDuplicateDomain(t *testing.T) {
	buffer := editor.NewBuffer("127.0.0.1 api.test\ninvalid-ip broken.test\n10.0.0.1 api.test\n# comment\n")
	diagnostics := HostsDiagnostics(buffer)
	if len(diagnostics) != 3 {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
	messages := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		messages = append(messages, diagnostic.Message)
	}
	joined := strings.Join(messages, "\n")
	for _, want := range []string{"malformed line", "duplicate domain"} {
		if !strings.Contains(joined, want) {
			t.Errorf("diagnostics do not contain %q: %s", want, joined)
		}
	}
}

func TestHostsLanguage_TokenizesCommentAndRule(t *testing.T) {
	language := HostsLanguage()
	if language == nil || language.LineComment != "#" {
		t.Fatalf("language = %+v", language)
	}
	tokenizer, ok := language.Tokenizer.(editor.TokenizerFunc)
	if !ok {
		t.Fatalf("tokenizer = %+v", language.Tokenizer)
	}
	tokens, _ := tokenizer.Tokenize("# comment", 0, nil)
	if len(tokens) != 1 || tokens[0].Kind != editor.TokenComment {
		t.Fatalf("comment tokens = %+v", tokens)
	}
	tokens, _ = tokenizer.Tokenize("127.0.0.1 api.test # local", 0, nil)
	if len(tokens) < 3 || tokens[len(tokens)-1].Kind != editor.TokenComment {
		t.Fatalf("rule tokens = %+v", tokens)
	}
}
