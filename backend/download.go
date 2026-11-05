package main

// Downloading of lent titles.
//
// Onleihe delivers titles either
//   - without hard DRM (e.g. watermarked EPUB/PDF, e-papers), or
//   - protected with Adobe Content Server DRM (an .acsm licence token).
//
// KOReader can open the first kind directly. Adobe-DRM files cannot be read
// by KOReader, and this project deliberately does NOT circumvent DRM. For such
// titles the .acsm licence file is stored so it can be opened with an
// Adobe-compatible reader, and the user is told why the book did not appear
// in the KOReader folder.

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type DownloadResult struct {
	OK      bool   `json:"ok"`
	Path    string `json:"path,omitempty"`
	DRM     bool   `json:"drm"`
	Message string `json:"message"`
}

type fileKind int

const (
	kindUnknown fileKind = iota
	kindEPUB
	kindPDF
	kindACSM
	kindDRMEPUB
	kindDRMPDF
)

func Download(c *Client, lendID, downloadDir, acsmDir, title, authors string) (*DownloadResult, error) {
	lic, err := c.PlayerLicence(lendID)
	if err != nil {
		return nil, err
	}
	base := safeName(title, authors, lendID)

	var acsm []byte
	for _, u := range []string{lic.EPUBURL, lic.PDFURL} {
		if u == "" || u == lic.ACSMURL {
			continue
		}
		data, ctype, err := c.Fetch(u)
		if err != nil {
			continue
		}
		switch classify(data, ctype) {
		case kindEPUB:
			return save(downloadDir, base+".epub", data)
		case kindPDF:
			return save(downloadDir, base+".pdf", data)
		case kindACSM:
			acsm = data
		}
	}

	if acsm == nil && lic.ACSMURL != "" {
		data, ctype, err := c.Fetch(lic.ACSMURL)
		if err != nil {
			return nil, err
		}
		switch classify(data, ctype) {
		case kindEPUB:
			return save(downloadDir, base+".epub", data)
		case kindPDF:
			return save(downloadDir, base+".pdf", data)
		default:
			acsm = data
		}
	}

	if acsm == nil {
		return &DownloadResult{OK: false, Message: "Für diese Ausleihe wurde keine Download-Datei gefunden."}, nil
	}
	if err := os.MkdirAll(acsmDir, 0o700); err != nil {
		return nil, err
	}
	p := filepath.Join(acsmDir, base+".acsm")
	if err := os.WriteFile(p, acsm, 0o600); err != nil {
		return nil, err
	}
	return &DownloadResult{
		OK:   false,
		DRM:  true,
		Path: p,
		Message: "Dieser Titel ist mit Adobe-DRM geschützt und kann von KOReader nicht geöffnet werden. " +
			"Die Lizenzdatei (.acsm) wurde gespeichert: " + p +
			" — sie kann mit Adobe Digital Editions oder einem anderen Adobe-kompatiblen Reader geöffnet werden.",
	}, nil
}

func save(dir, name string, data []byte) (*DownloadResult, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	p := filepath.Join(dir, name)
	tmp := p + ".part"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, p); err != nil {
		return nil, err
	}
	return &DownloadResult{OK: true, Path: p, Message: "Gespeichert: " + p + " — jetzt in KOReader öffnen."}, nil
}

func classify(data []byte, ctype string) fileKind {
	head := data
	if len(head) > 4096 {
		head = head[:4096]
	}
	ct := strings.ToLower(ctype)
	if strings.Contains(ct, "adobe.adept") || bytes.Contains(head, []byte("<fulfillmentToken")) {
		return kindACSM
	}
	if bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return kindUnknown
		}
		for _, f := range zr.File {
			name := strings.ToLower(f.Name)
			if name == "meta-inf/rights.xml" {
				return kindDRMEPUB
			}
			if name == "meta-inf/encryption.xml" {
				rc, err := f.Open()
				if err == nil {
					b, _ := io.ReadAll(io.LimitReader(rc, 1<<20))
					rc.Close()
					// Font obfuscation is fine; Adobe ADEPT is not.
					if bytes.Contains(b, []byte("ns.adobe.com/adept")) {
						return kindDRMEPUB
					}
				}
			}
		}
		return kindEPUB
	}
	if bytes.HasPrefix(data, []byte("%PDF")) {
		if bytes.Contains(data, []byte("EBX_HANDLER")) || bytes.Contains(data, []byte("ADEPT_LICENSE")) {
			return kindDRMPDF
		}
		return kindPDF
	}
	return kindUnknown
}

var unsafeChars = regexp.MustCompile(`[^\p{L}\p{N} ._,()'-]+`)

func safeName(title, authors, fallback string) string {
	name := strings.TrimSpace(title)
	if a := strings.TrimSpace(authors); a != "" {
		if i := strings.Index(a, ","); i > 0 {
			a = a[:i]
		}
		name = a + " - " + name
	}
	name = strings.TrimSpace(unsafeChars.ReplaceAllString(name, "_"))
	if name == "" || name == "-" {
		name = fmt.Sprintf("onleihe-%s", fallback)
	}
	if r := []rune(name); len(r) > 120 {
		name = string(r[:120])
	}
	return name
}
