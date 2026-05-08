package tts

import (
	"strings"
	"testing"
)

func TestChunkText(t *testing.T) {
	short := "Hello world.\n\nSecond paragraph."
	c := chunkText(short, 1000)
	if len(c) != 1 || c[0] != short {
		t.Fatalf("short text should produce a single chunk, got %v", c)
	}

	// 4 paragraphs of ~80 chars each — at maxLen=200 we should see multiple chunks.
	p := strings.Repeat("a ", 40)
	long := strings.Join([]string{p, p, p, p}, "\n\n")
	c = chunkText(long, 200)
	if len(c) < 2 {
		t.Fatalf("long text should split into multiple chunks, got %d", len(c))
	}
	for i, ch := range c {
		if len(ch) > 250 { // small slack for paragraph joins
			t.Fatalf("chunk %d too large: %d", i, len(ch))
		}
	}
}

func TestPreprocess(t *testing.T) {
	in := "Hello   world!\n\n\n\nSee https://example.com for details. [1] e.g. apples."
	out := Preprocess(in, PreprocessOpts{
		StripURLs:      true,
		StripCitations: true,
		NormalizeWS:    true,
		ExpandAbbrev:   true,
	})
	if strings.Contains(out, "https://") {
		t.Errorf("URL not stripped: %q", out)
	}
	if strings.Contains(out, "[1]") {
		t.Errorf("citation not stripped: %q", out)
	}
	if strings.Contains(out, "e.g.") {
		t.Errorf("abbreviation not expanded: %q", out)
	}
	if strings.Contains(out, "   ") {
		t.Errorf("whitespace not normalized: %q", out)
	}
}
