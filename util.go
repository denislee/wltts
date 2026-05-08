package main

import (
	"bytes"
	"time"
)

var zeroTime = time.Time{}

func byteSeeker(b []byte) *bytes.Reader {
	return bytes.NewReader(b)
}
