// Package tts implements a free Edge TTS client.
//
// It connects to Microsoft's free Edge "Read Aloud" WebSocket endpoint,
// which exposes a high-quality neural-voice TTS along with per-word
// timing metadata that we use to drive live highlighting in the UI.
package tts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

var (
	pathAudioMeta = []byte("Path:audio.metadata")
	pathTurnEnd   = []byte("Path:turn.end")
	headerSep     = []byte("\r\n\r\n")
)

const (
	trustedToken     = "6A5AA1D4EAFF4E9FB37E23D68491D6F4"
	wssURLBase       = "wss://speech.platform.bing.com/consumer/speech/synthesize/readaloud/edge/v1"
	chromiumFullVer  = "143.0.3650.75"
	chromiumMajorVer = "143"
	gecVersion       = "1-" + chromiumFullVer
	userAgent        = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" + chromiumMajorVer + ".0.0.0 Safari/537.36 Edg/" + chromiumMajorVer + ".0.0.0"
	winEpochSecs     = 11644473600 // seconds between 1601-01-01 (Windows epoch) and 1970-01-01 (Unix epoch)
)

// generateSecMSGEC computes the Sec-MS-GEC token Edge's TTS endpoint
// requires. It is sha256(<ticks_rounded_to_5min>+TrustedClientToken),
// uppercase hex. The ticks are 100-nanosecond intervals since the Windows
// filetime epoch, rounded down to the nearest 300_000_000_000 ticks
// (5 minutes), so the token is stable for ~5 minutes at a time.
func generateSecMSGEC() string {
	ticks := (time.Now().Unix() + winEpochSecs) * 10_000_000
	ticks -= ticks % 3_000_000_000
	sum := sha256.Sum256(fmt.Appendf(nil, "%d%s", ticks, trustedToken))
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

type WordBoundary struct {
	OffsetMs   int64  `json:"offset_ms"`
	DurationMs int64  `json:"duration_ms"`
	Text       string `json:"text"`
}

type Result struct {
	AudioMP3 []byte         `json:"-"`
	Words    []WordBoundary `json:"words"`
}

type rawMetadata struct {
	Metadata []struct {
		Type string `json:"Type"`
		Data struct {
			Offset   int64 `json:"Offset"`
			Duration int64 `json:"Duration"`
			Text     struct {
				Text string `json:"Text"`
			} `json:"text"`
		} `json:"Data"`
	} `json:"Metadata"`
}

// Synthesize sends a single text chunk and returns the produced MP3 audio
// together with word-boundary timings (offset/duration in milliseconds).
//
// rate uses Edge TTS prosody syntax, e.g. "+0%", "-20%", "+50%".
// pitch likewise, e.g. "+0Hz".
//
// If ctx is canceled the connection is closed and ctx.Err() is returned.
func Synthesize(ctx context.Context, text, voice, rate, pitch string) (*Result, error) {
	if voice == "" {
		voice = "en-US-AriaNeural"
	}
	if rate == "" {
		rate = "+0%"
	}
	if pitch == "" {
		pitch = "+0Hz"
	}

	connID := strings.ReplaceAll(uuid.NewString(), "-", "")
	wssURL := fmt.Sprintf(
		"%s?TrustedClientToken=%s&Sec-MS-GEC=%s&Sec-MS-GEC-Version=%s&ConnectionId=%s",
		wssURLBase, trustedToken, generateSecMSGEC(), gecVersion, connID)

	headers := http.Header{}
	headers.Set("Pragma", "no-cache")
	headers.Set("Cache-Control", "no-cache")
	headers.Set("Origin", "chrome-extension://jdiccldimpdaibmpdkjnbmckianbfold")
	headers.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	headers.Set("Accept-Language", "en-US,en;q=0.9")
	headers.Set("User-Agent", userAgent)

	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = 15 * time.Second
	conn, resp, err := dialer.DialContext(ctx, wssURL, headers)
	if err != nil {
		if resp != nil {
			body := make([]byte, 512)
			n, _ := resp.Body.Read(body)
			resp.Body.Close()
			return nil, fmt.Errorf("edge tts dial: %w (status=%s, body=%q)", err, resp.Status, string(body[:n]))
		}
		return nil, fmt.Errorf("edge tts dial: %w", err)
	}
	defer conn.Close()

	// Force the read loop to unblock when ctx is canceled. Closing the conn
	// makes the in-flight ReadMessage return an error; we then surface ctx.Err().
	stopWatcher := make(chan struct{})
	defer close(stopWatcher)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stopWatcher:
		}
	}()

	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")

	cfg := "X-Timestamp:" + timestamp + "\r\n" +
		"Content-Type:application/json; charset=utf-8\r\n" +
		"Path:speech.config\r\n\r\n" +
		`{"context":{"synthesis":{"audio":{"metadataoptions":{"sentenceBoundaryEnabled":"false","wordBoundaryEnabled":"true"},"outputFormat":"audio-24khz-48kbitrate-mono-mp3"}}}}`
	if err := conn.WriteMessage(websocket.TextMessage, []byte(cfg)); err != nil {
		return nil, fmt.Errorf("edge tts config: %w", err)
	}

	ssml := fmt.Sprintf(
		`<speak version='1.0' xmlns='http://www.w3.org/2001/10/synthesis' xml:lang='en-US'><voice name='%s'><prosody rate='%s' pitch='%s'>%s</prosody></voice></speak>`,
		voice, rate, pitch, html.EscapeString(text))

	req := "X-RequestId:" + connID + "\r\n" +
		"Content-Type:application/ssml+xml\r\n" +
		"X-Timestamp:" + timestamp + "\r\n" +
		"Path:ssml\r\n\r\n" + ssml
	if err := conn.WriteMessage(websocket.TextMessage, []byte(req)); err != nil {
		return nil, fmt.Errorf("edge tts ssml: %w", err)
	}

	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	result := &Result{}
	var audioBuf bytes.Buffer
	for {
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("edge tts read: %w", err)
		}
		switch msgType {
		case websocket.TextMessage:
			if bytes.Contains(data, pathAudioMeta) {
				idx := bytes.Index(data, headerSep)
				if idx == -1 {
					continue
				}
				var meta rawMetadata
				if err := json.Unmarshal(data[idx+4:], &meta); err != nil {
					continue
				}
				for _, m := range meta.Metadata {
					if m.Type != "WordBoundary" {
						continue
					}
					result.Words = append(result.Words, WordBoundary{
						OffsetMs:   m.Data.Offset / 10000,
						DurationMs: m.Data.Duration / 10000,
						Text:       m.Data.Text.Text,
					})
				}
			} else if bytes.Contains(data, pathTurnEnd) {
				result.AudioMP3 = audioBuf.Bytes()
				return result, nil
			}
		case websocket.BinaryMessage:
			if len(data) < 2 {
				continue
			}
			hdrLen := int(data[0])<<8 | int(data[1])
			if 2+hdrLen > len(data) {
				continue
			}
			audioBuf.Write(data[2+hdrLen:])
		}
	}
}

// SynthesizeLong chunks long text on paragraph/sentence boundaries (Edge TTS
// rejects very large requests), synthesizes each chunk, then stitches the
// audio and word-boundary timings together. Word offsets in the returned
// Result are in the timeline of the concatenated MP3.
//
// If progress is non-nil, it is called with (done, total) before each chunk
// is synthesized and once more when all chunks complete. Cancellation via
// ctx is honored between and during chunks.
func SynthesizeLong(ctx context.Context, text, voice, rate, pitch string, progress func(done, total int)) (*Result, error) {
	raw := chunkText(text, 2800)
	chunks := raw[:0]
	for _, c := range raw {
		if strings.TrimSpace(c) != "" {
			chunks = append(chunks, c)
		}
	}
	final := &Result{}
	var audioBuf bytes.Buffer
	var offsetMs int64
	total := len(chunks)
	if progress != nil {
		progress(0, total)
	}
	for i, chunk := range chunks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r, err := Synthesize(ctx, chunk, voice, rate, pitch)
		if err != nil {
			return nil, err
		}
		for _, w := range r.Words {
			final.Words = append(final.Words, WordBoundary{
				OffsetMs:   w.OffsetMs + offsetMs,
				DurationMs: w.DurationMs,
				Text:       w.Text,
			})
		}
		if n := len(r.Words); n > 0 {
			last := r.Words[n-1]
			offsetMs += last.OffsetMs + last.DurationMs + 250
		}
		audioBuf.Write(r.AudioMP3)
		if progress != nil {
			progress(i+1, total)
		}
	}
	final.AudioMP3 = audioBuf.Bytes()
	return final, nil
}

func chunkText(text string, maxLen int) []string {
	text = strings.TrimSpace(text)
	if len(text) <= maxLen {
		return []string{text}
	}
	var chunks []string
	var cur strings.Builder
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			chunks = append(chunks, s)
		}
		cur.Reset()
	}
	for _, p := range strings.Split(text, "\n\n") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if len(p) > maxLen {
			for _, s := range splitSentences(p) {
				if cur.Len()+len(s)+1 > maxLen && cur.Len() > 0 {
					flush()
				}
				if cur.Len() > 0 {
					cur.WriteByte(' ')
				}
				cur.WriteString(s)
			}
			continue
		}
		if cur.Len()+len(p)+2 > maxLen && cur.Len() > 0 {
			flush()
		}
		if cur.Len() > 0 {
			cur.WriteString("\n\n")
		}
		cur.WriteString(p)
	}
	flush()
	return chunks
}

func splitSentences(p string) []string {
	var out []string
	start := 0
	for i, r := range p {
		if r != '.' && r != '!' && r != '?' && r != '。' {
			continue
		}
		next := i + utf8.RuneLen(r)
		if next < len(p) && (p[next] == ' ' || p[next] == '\n') {
			if s := strings.TrimSpace(p[start:next]); s != "" {
				out = append(out, s)
			}
			start = next
		}
	}
	if s := strings.TrimSpace(p[start:]); s != "" {
		out = append(out, s)
	}
	return out
}

