package main

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"syscall"
	"testing"
)

func TestAppLoadRoundTrip(t *testing.T) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_SEQPACKET, 0)
	if err != nil {
		t.Skipf("SOCK_SEQPACKET not supported here: %v", err)
	}
	a := &AppLoad{fd: fds[0]}
	b := &AppLoad{fd: fds[1]}
	defer a.Close()
	defer b.Close()

	if err := a.Send(4, `{"query":"Kafka"}`); err != nil {
		t.Fatal(err)
	}
	if err := a.Send(msgSystemTerminate, ""); err != nil {
		t.Fatal(err)
	}
	typ, body, err := b.Receive()
	if err != nil || typ != 4 || body != `{"query":"Kafka"}` {
		t.Fatalf("got %d %q %v", typ, body, err)
	}
	typ, body, err = b.Receive()
	if err != nil || typ != msgSystemTerminate || body != "" {
		t.Fatalf("got %d %q %v", typ, body, err)
	}
}

func makeZip(t *testing.T, files map[string]string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(content))
	}
	zw.Close()
	return buf.Bytes()
}

func TestClassify(t *testing.T) {
	plain := makeZip(t, map[string]string{"mimetype": "application/epub+zip", "META-INF/container.xml": "<x/>"})
	drm := makeZip(t, map[string]string{"mimetype": "application/epub+zip", "META-INF/rights.xml": "<x/>"})
	fontObf := makeZip(t, map[string]string{"META-INF/encryption.xml": `<enc algorithm="http://www.idpf.org/2008/embedding"/>`})
	adept := makeZip(t, map[string]string{"META-INF/encryption.xml": `<x xmlns="http://ns.adobe.com/adept"/>`})

	cases := []struct {
		name  string
		data  []byte
		ctype string
		want  fileKind
	}{
		{"epub", plain, "application/epub+zip", kindEPUB},
		{"drm epub", drm, "", kindDRMEPUB},
		{"font obfuscation", fontObf, "", kindEPUB},
		{"adept encryption", adept, "", kindDRMEPUB},
		{"pdf", []byte("%PDF-1.7 hello"), "", kindPDF},
		{"drm pdf", []byte("%PDF-1.7 /Filter /EBX_HANDLER"), "", kindDRMPDF},
		{"acsm body", []byte(`<?xml version="1.0"?><fulfillmentToken xmlns="http://ns.adobe.com/adept">`), "", kindACSM},
		{"acsm ctype", []byte("x"), "application/vnd.adobe.adept+xml", kindACSM},
	}
	for _, c := range cases {
		if got := classify(c.data, c.ctype); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestParseMedia(t *testing.T) {
	m := parseMedia(map[string]any{
		"productId": "123",
		"product": map[string]any{
			"id": "123", "title": "Der Process", "mediaType": "EBOOK",
			"authors": []any{map[string]any{"firstName": "Franz", "lastName": "Kafka"}},
		},
		"userAction":      []any{"LEND"},
		"userInformation": map[string]any{"lend": map[string]any{"lendId": "L1"}},
	})
	if m.ProductID != "123" || m.Title != "Der Process" || m.Authors != "Franz Kafka" || m.LendID != "L1" || !m.CanLend {
		t.Fatalf("unexpected %+v", m)
	}
}

func TestLicenceURLs(t *testing.T) {
	p := map[string]any{
		"drmInformation": []any{
			map[string]any{"drmSystem": "ACS", "uri": "https://acs.example/x.acsm"},
			map[string]any{"mimeType": "EPUB", "uri": "https://dl.example/book.epub"},
		},
	}
	if got := findDRMURI(p, "ACS"); got != "https://acs.example/x.acsm" {
		t.Fatal(got)
	}
	if got := findMimeURI(p, "EPUB"); got != "https://dl.example/book.epub" {
		t.Fatal(got)
	}
	if got := findMimeURI(p, "PDF"); got != "" {
		t.Fatal(got)
	}
}

func TestJWTClaims(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"uid":"u-1","pid":"master","lid":"42"}`))
	c := jwtClaims("h." + payload + ".s")
	if c["uid"] != "u-1" || c["lid"] != "42" {
		t.Fatalf("%v", c)
	}
}

func TestSafeName(t *testing.T) {
	if got := safeName("Der Process: Roman", "Franz Kafka, Max Brod", "1"); got != "Franz Kafka - Der Process_ Roman" {
		t.Fatal(got)
	}
	if got := safeName("", "", "77"); got != "onleihe-77" {
		t.Fatal(got)
	}
}
