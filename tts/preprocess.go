package tts

import (
	"regexp"
	"strings"
)

type PreprocessOpts struct {
	StripURLs      bool              `json:"strip_urls"`
	StripCitations bool              `json:"strip_citations"`
	StripEmoji     bool              `json:"strip_emoji"`
	NormalizeWS    bool              `json:"normalize_whitespace"`
	ExpandAbbrev   bool              `json:"expand_abbreviations"`
	Replacements   map[string]string `json:"replacements"`
}

var (
	urlRe      = regexp.MustCompile(`https?://\S+`)
	citationRe = regexp.MustCompile(`\[\s*\d+\s*\]|\[\s*citation needed\s*\]|\[\s*edit\s*\]`)
	emojiRe    = regexp.MustCompile(`[\x{1F300}-\x{1FAFF}\x{2600}-\x{27BF}\x{1F000}-\x{1F2FF}]`)
	multiWS    = regexp.MustCompile(`[ \t]+`)
	multiNL    = regexp.MustCompile(`\n{3,}`)
)

var commonAbbrev = map[string]string{
	" e.g.":  " for example",
	" i.e.":  " that is",
	" etc.":  " et cetera",
	" vs.":   " versus",
	" Dr.":   " Doctor",
	" Mr.":   " Mister",
	" Mrs.":  " Misses",
	" Ms.":   " Miss",
	" St.":   " Saint",
	" approx.": " approximately",
}

// Preprocess applies the configured cleanups in a fixed, predictable order.
// User-supplied replacements run last so they always take precedence.
func Preprocess(text string, opts PreprocessOpts) string {
	if opts.StripURLs {
		text = urlRe.ReplaceAllString(text, "")
	}
	if opts.StripCitations {
		text = citationRe.ReplaceAllString(text, "")
	}
	if opts.StripEmoji {
		text = emojiRe.ReplaceAllString(text, "")
	}
	if opts.ExpandAbbrev {
		for k, v := range commonAbbrev {
			text = strings.ReplaceAll(text, k, v)
		}
	}
	for k, v := range opts.Replacements {
		if k == "" {
			continue
		}
		text = strings.ReplaceAll(text, k, v)
	}
	if opts.NormalizeWS {
		text = multiWS.ReplaceAllString(text, " ")
		text = multiNL.ReplaceAllString(text, "\n\n")
		var lines []string
		for _, l := range strings.Split(text, "\n") {
			lines = append(lines, strings.TrimSpace(l))
		}
		text = strings.Join(lines, "\n")
	}
	return strings.TrimSpace(text)
}
