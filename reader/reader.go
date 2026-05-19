// Package reader extracts the readable body of a web page, stripping nav,
// ads, sidebars, and other non-article chrome. It wraps go-readability
// (the Go port of Mozilla's Readability.js).
package reader

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	readability "github.com/go-shiori/go-readability"
)

var multiNewline = regexp.MustCompile(`\n{3,}`)

type Article struct {
	URL      string `json:"url"`
	Title    string `json:"title"`
	Byline   string `json:"byline"`
	SiteName string `json:"site_name"`
	HTML     string `json:"html"`
	Text     string `json:"text"`
}

const browserUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"

var httpClient = &http.Client{Timeout: 30 * time.Second}

func isMediumHost(host string) bool {
	host = strings.ToLower(host)
	return host == "medium.com" || strings.HasSuffix(host, ".medium.com")
}

func Fetch(rawURL string) (*Article, error) {
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "https://" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}
	if isMediumHost(u.Host) {
		rawURL = "https://freedium-mirror.cfd/" + u.String()
		u, err = url.Parse(rawURL)
		if err != nil {
			return nil, fmt.Errorf("invalid url: %w", err)
		}
	}

	// Many sites (Wikipedia, news sites) reject Go's default User-Agent or
	// return JSON instead of HTML for it, so we fetch ourselves with a
	// browser UA and feed the body to readability.FromReader.
	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	art, err := readability.FromReader(bytes.NewReader(body), u)
	if err != nil {
		return nil, fmt.Errorf("readability: %w", err)
	}
	text := multiNewline.ReplaceAllString(strings.TrimSpace(art.TextContent), "\n\n")
	return &Article{
		URL:      u.String(),
		Title:    art.Title,
		Byline:   art.Byline,
		SiteName: art.SiteName,
		HTML:     art.Content,
		Text:     text,
	}, nil
}
