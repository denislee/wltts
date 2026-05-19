package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"wltts/reader"
	"wltts/tts"
)

//go:embed all:frontend
var frontendFS embed.FS

type fetchResp struct {
	Article *reader.Article `json:"article"`
}

type synthReq struct {
	Text  string `json:"text"`
	Voice string `json:"voice"`
	Rate  string `json:"rate"`
	Pitch string `json:"pitch"`
}

type preprocessReq struct {
	Text string             `json:"text"`
	Opts tts.PreprocessOpts `json:"opts"`
}

type preprocessResp struct {
	Text string `json:"text"`
}

// audioCache holds the most recent synthesis result. We serve the audio via
// a separate /audio endpoint instead of embedding a megabytes-large base64
// blob in the JSON response — the <audio> element streams it natively.
type audioCache struct {
	mu  sync.RWMutex
	mp3 []byte
}

func (c *audioCache) set(b []byte) {
	c.mu.Lock()
	c.mp3 = b
	c.mu.Unlock()
}

func (c *audioCache) get() []byte {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.mp3
}

func newServer() (*http.Server, string, error) {
	mux := http.NewServeMux()
	cache := &audioCache{}

	sub, err := fs.Sub(frontendFS, "frontend")
	if err != nil {
		return nil, "", err
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))

	voicesJSON, err := json.Marshal(tts.Voices)
	if err != nil {
		return nil, "", err
	}
	mux.HandleFunc("/api/voices", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(voicesJSON)
	})

	mux.HandleFunc("/api/fetch", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct{ URL string `json:"url"` }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		art, err := reader.Fetch(strings.TrimSpace(body.URL))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, fetchResp{Article: art})
	})

	mux.HandleFunc("/api/preprocess", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body preprocessReq
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, preprocessResp{Text: tts.Preprocess(body.Text, body.Opts)})
	})

	// /api/synthesize streams NDJSON events while audio is generated:
	//   {"type":"progress","done":N,"total":M}
	//   {"type":"result","words":[...],"audio_url":"/audio.mp3","duration_ms":N}
	//   {"type":"error","message":"..."}
	// Closing the request connection cancels the in-flight synthesis.
	mux.HandleFunc("/api/synthesize", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body synthReq
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(body.Text) == "" {
			http.Error(w, "empty text", http.StatusBadRequest)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		enc := json.NewEncoder(w)
		emit := func(v any) {
			_ = enc.Encode(v)
			flusher.Flush()
		}

		progress := func(done, total int) {
			emit(map[string]any{"type": "progress", "done": done, "total": total})
		}

		res, err := tts.SynthesizeLong(r.Context(), body.Text, body.Voice, body.Rate, body.Pitch, progress)
		if err != nil {
			if r.Context().Err() != nil {
				return // client canceled — silently stop
			}
			emit(map[string]any{"type": "error", "message": err.Error()})
			return
		}
		cache.set(res.AudioMP3)
		var dur int64
		if n := len(res.Words); n > 0 {
			last := res.Words[n-1]
			dur = last.OffsetMs + last.DurationMs
		}
		emit(map[string]any{
			"type":        "result",
			"words":       res.Words,
			"audio_url":   "/audio.mp3",
			"duration_ms": dur,
		})
	})

	cfgStore := newConfigStore()
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(cfgStore.read())
		case http.MethodPost:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if !json.Valid(body) {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			cfgStore.write(body)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/audio.mp3", func(w http.ResponseWriter, r *http.Request) {
		mp3 := cache.get()
		if len(mp3) == 0 {
			http.Error(w, "no audio", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Accept-Ranges", "bytes")
		http.ServeContent(w, r, "audio.mp3", time.Time{}, bytes.NewReader(mp3))
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	return srv, "http://" + ln.Addr().String(), nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// configStore persists the frontend's user preferences (voice, rate, last URL,
// preprocess options) to a JSON file under the user's config dir so they
// survive across app restarts. The shape is opaque — the frontend owns it.
type configStore struct {
	path string
	mu   sync.Mutex
}

func newConfigStore() *configStore {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		dir = filepath.Join(os.TempDir(), "wltts")
	} else {
		dir = filepath.Join(dir, "wltts")
	}
	_ = os.MkdirAll(dir, 0o755)
	return &configStore{path: filepath.Join(dir, "config.json")}
}

func (c *configStore) read() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, err := os.ReadFile(c.path)
	if err != nil || len(b) == 0 || !json.Valid(b) {
		return []byte("{}")
	}
	return b
}

func (c *configStore) write(b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, c.path)
}
