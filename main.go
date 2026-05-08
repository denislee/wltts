package main

import (
	"flag"
	"fmt"
	"log"

	webview "github.com/webview/webview_go"
)

func main() {
	serverOnly := flag.Bool("server", false, "Run only the HTTP server (no GUI window). Prints the URL on stdout.")
	flag.Parse()

	srv, addr, err := newServer()
	if err != nil {
		log.Fatalf("server: %v", err)
	}
	defer srv.Close()

	if *serverOnly {
		fmt.Println(addr)
		select {} // block forever
	}

	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("WLTTS — Web Reader TTS")
	w.SetSize(1180, 800, webview.HintNone)
	w.Navigate(addr)
	w.Run()
}
