package main

// Best-effort detection of a title's copy protection from Onleihe catalogue
// metadata, so the UI can mark DRM-free titles before they are borrowed.
//
// The Onleihe API is undocumented and the exact field names vary, so this
// looks for typical DRM/protection fields anywhere in the product object.
// The result is "free", "drm" or "" (unknown). Unknown titles are never
// hidden. Set RM_ONLEIHE_DEBUG=1 to log the raw product JSON of search hits
// whose protection could not be determined.

import (
	"strings"
)

const (
	drmUnknown = ""
	drmFree    = "free"
	drmAdobe   = "drm"
)

// classifyProtection maps a protection/DRM value to free/drm/unknown.
func classifyProtection(v string) string {
	s := strings.ToUpper(strings.TrimSpace(v))
	switch {
	case s == "":
		return drmUnknown
	case strings.Contains(s, "WATERMARK"), strings.Contains(s, "WASSERZEICHEN"),
		s == "NONE", s == "NO", s == "FALSE", strings.Contains(s, "DRM_FREE"),
		strings.Contains(s, "DRMFREE"), strings.Contains(s, "DRM-FREE"), s == "FREE",
		strings.Contains(s, "SOCIAL"), strings.Contains(s, "OHNE"):
		return drmFree
	case strings.Contains(s, "ADOBE"), strings.Contains(s, "ACS"), strings.Contains(s, "ADEPT"),
		strings.Contains(s, "HARD"), strings.Contains(s, "CARE"), s == "DRM", s == "TRUE", s == "YES":
		return drmAdobe
	}
	return drmUnknown
}

func isProtectionKey(k string) bool {
	k = strings.ToLower(k)
	return strings.Contains(k, "drm") || strings.Contains(k, "protection") ||
		strings.Contains(k, "kopierschutz") || k == "watermark" || k == "watermarked"
}

// detectDRM inspects product metadata. Any field that clearly indicates hard
// DRM wins over fields indicating a watermark, so mixed signals count as DRM.
func detectDRM(p any) string {
	found := drmUnknown
	var walk func(any, int)
	walk = func(p any, depth int) {
		if depth > 6 || found == drmAdobe {
			return
		}
		switch v := p.(type) {
		case map[string]any:
			if cls := strings.ToLower(str(v["_class"])); strings.HasSuffix(cls, "drminformation") {
				if r := classifyProtection(firstStr(v["drmSystem"], v["type"])); r != drmUnknown {
					note(&found, r)
				} else if !strings.Contains(cls, "watermark") {
					note(&found, drmAdobe)
				} else {
					note(&found, drmFree)
				}
			}
			for k, x := range v {
				if !isProtectionKey(k) {
					walk(x, depth+1)
					continue
				}
				lk := strings.ToLower(k)
				switch val := x.(type) {
				case bool:
					switch {
					case strings.Contains(lk, "free") || strings.Contains(lk, "watermark"):
						if val {
							note(&found, drmFree)
						}
					case strings.HasPrefix(lk, "is") || strings.HasPrefix(lk, "has") || lk == "drm" || lk == "drmprotected":
						if val {
							note(&found, drmAdobe)
						} else {
							note(&found, drmFree)
						}
					}
				case string:
					note(&found, classifyProtection(val))
				case []any:
					for _, it := range val {
						if s, ok := it.(string); ok {
							note(&found, classifyProtection(s))
						} else {
							walk(it, depth+1)
						}
					}
				default:
					walk(x, depth+1)
				}
			}
		case []any:
			for _, x := range v {
				walk(x, depth+1)
			}
		}
	}
	walk(p, 0)
	return found
}

func note(found *string, r string) {
	if r == drmAdobe || (r == drmFree && *found == drmUnknown) {
		*found = r
	}
}
