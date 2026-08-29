package main

import (
	"encoding/json"
	"net"
	"net/url"
	"os"
	"testing"
)

func TestWriteReadyUsesBoundPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeReady(writer, listener.Addr().String()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	var ready struct {
		Type       string `json:"type"`
		APIBaseURL string `json:"apiBaseUrl"`
	}
	if err := json.NewDecoder(reader).Decode(&ready); err != nil {
		t.Fatal(err)
	}
	reader.Close()
	if ready.Type != "ready" {
		t.Fatalf("ready type=%q", ready.Type)
	}
	parsed, err := url.Parse(ready.APIBaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "http" || parsed.Host != listener.Addr().String() {
		t.Fatalf("ready URL=%q listener=%q", ready.APIBaseURL, listener.Addr())
	}
}
