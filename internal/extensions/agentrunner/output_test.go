package agentrunner

import (
	"bytes"
	"strings"
	"testing"
)

// The model reads the end of a long command's output, where the failure and
// the summary are. A cut from the start would keep the noise and drop them.
func TestOutputTailKeepsTheEnd(t *testing.T) {
	o := newOutputTail(10, nil, 0)
	o.Write([]byte(strings.Repeat("a", 30)))
	o.Write([]byte("FAIL: TestThing"))
	text, truncated := o.result()
	if !truncated {
		t.Fatal("output over the limit not marked truncated")
	}
	if text != " TestThing" {
		t.Fatalf("kept %q, want the last 10 bytes", text)
	}
}

func TestOutputTailUnderLimitIsWhole(t *testing.T) {
	o := newOutputTail(100, nil, 0)
	o.Write([]byte("ok\n"))
	text, truncated := o.result()
	if truncated || text != "ok\n" {
		t.Fatalf("result = %q truncated=%v", text, truncated)
	}
}

// A cut that lands inside a multi-byte character must not leave a broken
// rune at the start of the text the model reads.
func TestOutputTailDoesNotSplitACharacter(t *testing.T) {
	o := newOutputTail(4, nil, 0)
	o.Write([]byte("xé€é")) // é is two bytes, € is three
	text, _ := o.result()
	if strings.ContainsRune(text, '�') || !strings.HasSuffix("xé€é", text) {
		t.Fatalf("result %q is not a clean suffix", text)
	}
}

// Every byte reaches the full copy until its cap, and the copy says it is
// incomplete when it stops short.
func TestOutputTailFullCopyIsCappedAndSaysSo(t *testing.T) {
	var file bytes.Buffer
	o := newOutputTail(4, &file, 6)
	o.Write([]byte("0123"))
	o.Write([]byte("456789"))
	if file.String() != "012345" {
		t.Fatalf("full copy = %q, want the first 6 bytes", file.String())
	}
	if o.complete() {
		t.Fatal("capped copy reported complete")
	}
}

func TestOutputTailFullCopyCompleteUnderCap(t *testing.T) {
	var file bytes.Buffer
	o := newOutputTail(4, &file, 100)
	o.Write([]byte("all of it"))
	if file.String() != "all of it" || !o.complete() {
		t.Fatalf("full copy = %q complete=%v", file.String(), o.complete())
	}
}
