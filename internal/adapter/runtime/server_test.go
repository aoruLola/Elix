package runtime

import (
	"strings"
	"sync"
	"testing"
)

func TestScanPipeHandlesLongLines(t *testing.T) {
	line := strings.Repeat("x", 200*1024)
	input := line + "\n"

	var wg sync.WaitGroup
	var got []string
	wg.Add(1)
	go scanPipe(strings.NewReader(input), func(s string) {
		got = append(got, s)
	}, &wg)
	wg.Wait()

	if len(got) != 1 {
		t.Fatalf("expected 1 line, got %d", len(got))
	}
	if got[0] != line {
		t.Fatalf("line mismatch: got=%d chars want=%d chars", len(got[0]), len(line))
	}
}

