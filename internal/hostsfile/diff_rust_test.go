package hostsfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Rust/Go 差分测试：同一语料输入，Go Parse 的结构化输出必须与
// 源项目 Rust parser（mHost ef1502e，mhost-hosts crate）的输出一致。
// golden 由 tools/rust-harness/generate.sh 生成（见 fixtures/hosts/rust-golden）。

type outRule struct {
	Line        int      `json:"line"`
	IP          *string  `json:"ip"`
	Domains     []string `json:"domains"`
	Comment     *string  `json:"comment"`
	CommentOnly bool     `json:"comment_only"`
	Enabled     bool     `json:"enabled"`
}

type outError struct {
	Line  int    `json:"line"`
	Kind  string `json:"kind"`
	Token string `json:"token"`
}

type outDuplicate struct {
	Domain string `json:"domain"`
	Lines  []int  `json:"lines"`
	Kind   string `json:"kind"`
}

type outResult struct {
	Rules      []outRule      `json:"rules"`
	Errors     []outError     `json:"errors"`
	Duplicates []outDuplicate `json:"duplicates"`
}

func goResult(input string) outResult {
	parsed := Parse(input)
	result := outResult{}
	for _, rule := range parsed.Rules {
		var ip *string
		if rule.IP != nil {
			text := rule.IP.String()
			ip = &text
		}
		domains := rule.Domains
		if domains == nil {
			domains = []string{}
		}
		result.Rules = append(result.Rules, outRule{
			Line:        rule.Line,
			IP:          ip,
			Domains:     domains,
			Comment:     rule.Comment,
			CommentOnly: rule.CommentOnly,
			Enabled:     rule.Enabled,
		})
	}
	for _, err := range parsed.Errors {
		result.Errors = append(result.Errors, outError{Line: err.Line, Kind: err.Kind.String(), Token: err.Token})
	}
	for _, dup := range parsed.Duplicates {
		result.Duplicates = append(result.Duplicates, outDuplicate{Domain: dup.Domain, Lines: dup.Lines, Kind: dup.Kind.String()})
	}
	if result.Rules == nil {
		result.Rules = []outRule{}
	}
	if result.Errors == nil {
		result.Errors = []outError{}
	}
	if result.Duplicates == nil {
		result.Duplicates = []outDuplicate{}
	}
	return result
}

func TestDiffAgainstRustGoldens(t *testing.T) {
	corpusDir := filepath.Join("..", "..", "fixtures", "hosts", "corpus")
	goldenDir := filepath.Join("..", "..", "fixtures", "hosts", "rust-golden")

	entries, err := os.ReadDir(corpusDir)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("corpus is empty")
	}

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".txt") {
			continue
		}
		t.Run(name, func(t *testing.T) {
			input, err := os.ReadFile(filepath.Join(corpusDir, name))
			if err != nil {
				t.Fatalf("read input: %v", err)
			}
			golden, err := os.ReadFile(filepath.Join(goldenDir, strings.TrimSuffix(name, ".txt")+".json"))
			if err != nil {
				t.Fatalf("read golden (regenerate with tools/rust-harness/generate.sh): %v", err)
			}

			var want any
			if err := json.Unmarshal(golden, &want); err != nil {
				t.Fatalf("decode golden: %v", err)
			}
			gotBytes, err := json.Marshal(goResult(string(input)))
			if err != nil {
				t.Fatalf("marshal go result: %v", err)
			}
			var got any
			if err := json.Unmarshal(gotBytes, &got); err != nil {
				t.Fatalf("decode go result: %v", err)
			}

			if !reflect.DeepEqual(got, want) {
				gotPretty, _ := json.MarshalIndent(got, "", "  ")
				t.Fatalf("Go parser diverges from Rust golden %s\ngot:\n%s", name, gotPretty)
			}
		})
	}
}
