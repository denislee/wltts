# wltts

Web-reader text-to-speech in a native Go window. Paste a URL, see the page in
read mode, and hear it read aloud with the current paragraph and word
highlighted live.

- TTS: Microsoft Edge "Read Aloud" — free, neural voices, with word-boundary
  timings used for live highlighting. No API key.
- Read mode: go-readability (Mozilla Readability port).
- GUI: native webview (webview_go) wrapping a local HTTP server.

## Build

System deps (Linux): `gtk3`, `webkit2gtk-4.1`, `pkg-config`.

```sh
PKG_CONFIG_PATH="$PWD/build/pkgconfig:$PKG_CONFIG_PATH" go build -o wltts .
./wltts
```

The `build/pkgconfig/` shim is only needed where the system has
`webkit2gtk-4.1` instead of `4.0` (e.g. Arch, recent Ubuntu); the two are
ABI-compatible.

For a headless run (debug API only, no window):

```sh
./wltts -server
```

## Features

- URL → read-mode extraction.
- Voice picker (en, pt-BR, pt-PT, es, fr, de, it, ja, ko, zh).
- Speed slider (-50% to +80%).
- Live paragraph + word highlighting, auto-scroll.
- Preprocess panel: strip URLs / citations / emoji, normalize whitespace,
  expand common abbreviations (e.g. → for example), free-form
  `find=>replace` rules, and direct text editing before synthesis.
