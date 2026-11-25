package hostsfixture

import (
	"bytes"
	"strings"
	"testing"
)

func TestGenerate_DeterministicAndExactLineCount(t *testing.T) {
	var first bytes.Buffer
	if err := Generate(&first, 25); err != nil {
		t.Fatal(err)
	}
	var second bytes.Buffer
	if err := Generate(&second, 25); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() {
		t.Fatal("generated content differs between runs")
	}
	if got := strings.Count(first.String(), "\n"); got != 25 {
		t.Fatalf("line count = %d, want 25", got)
	}
	if !strings.HasPrefix(first.String(), "# synthetic hosts block 0\n") {
		t.Fatalf("unexpected prefix %q", first.String())
	}
	if !strings.Contains(first.String(), " host-000024.phase0.test\n") {
		t.Fatalf("missing final generated host:\n%s", first.String())
	}
}

func TestGenerate_LargeSmokeSample(t *testing.T) {
	var output bytes.Buffer
	if err := Generate(&output, 100000); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(output.String(), "\n"); got != 100000 {
		t.Fatalf("line count = %d, want 100000", got)
	}
}

func TestGenerate_RejectsInvalidInput(t *testing.T) {
	for _, lines := range []int{-1, 0} {
		if err := Generate(&bytes.Buffer{}, lines); err == nil {
			t.Errorf("Generate(lines=%d) returned nil error", lines)
		}
	}
}
