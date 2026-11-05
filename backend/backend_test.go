package main

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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

func TestDetectDRM(t *testing.T) {
	cases := []struct {
		name string
		p    any
		want string
	}{
		{"adobe string", map[string]any{"drmType": "ADOBE_ACS"}, drmAdobe},
		{"watermark string", map[string]any{"copyProtection": "WATERMARK"}, drmFree},
		{"none", map[string]any{"drm": "NONE"}, drmFree},
		{"bool protected", map[string]any{"isDrmProtected": true}, drmAdobe},
		{"bool not protected", map[string]any{"isDrmProtected": false}, drmFree},
		{"nested formats", map[string]any{"formats": []any{
			map[string]any{"mimeType": "EPUB", "drmSystem": "ACS"},
		}}, drmAdobe},
		{"drm class", map[string]any{"x": map[string]any{"_class": "de.divibib.AcsDrmInformation"}}, drmAdobe},
		{"mixed wins drm", map[string]any{"watermark": "yes", "drmSystem": "ADOBE"}, drmAdobe},
		{"no info", map[string]any{"title": "Der Process", "mediaType": "EBOOK"}, drmUnknown},
	}
	for _, c := range cases {
		if got := detectDRM(c.p); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestLibrariesPlainArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/management/v1/auth/domains":
			w.Write([]byte(`[{"host":"x.onleihe.de","onleiheId":"O1","onleiheName":"X","libraryId":null,"libraryName":null}]`))
		case "/user-application/v2/auth/libraries":
			w.Write([]byte(`{"page":1,"totalPages":1,"content":[{"id":"L1","onleiheId":"O1","name":"Stadtbibliothek","city":"Bern"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	libs, err := NewClient("x.onleihe.de").Libraries()
	if err != nil {
		t.Fatal(err)
	}
	if len(libs) != 1 || libs[0].LibraryID != "L1" || libs[0].OnleiheID != "O1" || libs[0].City != "Bern" {
		t.Fatalf("unexpected %+v", libs)
	}
}

func fakeJWT(claims string) string {
	return "h." + base64.RawURLEncoding.EncodeToString([]byte(claims)) + ".s"
}

// An expired access token without a refresh token is renewed with the access
// token itself, and the request is retried.
func TestRefreshWithAccessTokenOn401(t *testing.T) {
	fresh := fakeJWT(`{"uid":"u-1","exp":9999999999}`)
	var refreshBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user-application/v1/auth/refresh":
			b, _ := io.ReadAll(r.Body)
			refreshBody = string(b)
			w.Write([]byte(`{"accessToken":"` + fresh + `"}`))
		case "/thing":
			if r.Header.Get("Authorization") != "Bearer "+fresh {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer srv.Close()
	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	c := NewClient("x.onleihe.de")
	c.Session = Session{AccessToken: "old", UserID: "u-1"}
	if _, err := c.do("GET", "/thing", nil, nil, true); err != nil {
		t.Fatal(err)
	}
	if refreshBody != `{"token":"old"}` {
		t.Fatalf("refresh body %q", refreshBody)
	}
	if c.Session.AccessToken != fresh || c.Session.ExpiresAt != 9999999999 {
		t.Fatalf("session not updated: %+v", c.Session)
	}
}

// If renewing fails, the session is dropped but the library is kept.
func TestSessionExpiredKeepsLibrary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	c := NewClient("x.onleihe.de")
	c.OnleiheID, c.LibraryID = "O1", "L1"
	c.Session = Session{AccessToken: "old", RefreshToken: "r", UserID: "u-1"}
	saved := 0
	c.OnSession = func() { saved++ }
	_, err := c.do("GET", "/thing", nil, nil, true)
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("got %v", err)
	}
	if c.LoggedIn() || c.OnleiheID != "O1" || c.LibraryID != "L1" || saved == 0 {
		t.Fatalf("unexpected state: %+v %s %s saved=%d", c.Session, c.OnleiheID, c.LibraryID, saved)
	}
}

// A token that is about to expire is renewed before the request is sent.
func TestProactiveRefresh(t *testing.T) {
	fresh := fakeJWT(`{"uid":"u-1","exp":9999999999}`)
	refreshed := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user-application/v1/auth/refresh" {
			refreshed = true
			w.Write([]byte(`{"accessToken":"` + fresh + `"}`))
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	c := NewClient("x.onleihe.de")
	c.Session = Session{AccessToken: "old", RefreshToken: "r", UserID: "u-1", ExpiresAt: 1}
	if _, err := c.do("GET", "/thing", nil, nil, true); err != nil {
		t.Fatal(err)
	}
	if !refreshed || c.Session.AccessToken != fresh {
		t.Fatalf("not refreshed: %+v", c.Session)
	}
}
