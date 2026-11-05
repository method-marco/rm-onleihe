package main

// Client for the Onleihe "v3" JSON API (api.onleihe.de), as used by the
// current Onleihe web frontend. Endpoint paths and JSON key names follow the
// MIT-licensed Onleiharr project (github.com/nzb-tuxxx/Onleiharr). The API is
// undocumented, so all response parsing is deliberately tolerant.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var apiBase = "https://api.onleihe.de" // var so tests can point it at a fake server

var ErrAuth = errors.New("not logged in")

// ErrSessionExpired means the session could not be renewed; the user has to
// enter the password again. The selected library is kept.
var ErrSessionExpired = errors.New("session expired")

type Session struct {
	AccessToken  string `json:"accessToken,omitempty"`
	RefreshToken string `json:"refreshToken,omitempty"`
	TokenType    string `json:"tokenType,omitempty"`
	UserID       string `json:"userId,omitempty"`
	ProfileID    string `json:"profileId,omitempty"`
	ExpiresAt    int64  `json:"expiresAt,omitempty"` // access token expiry (unix seconds, from JWT "exp")
}

type Client struct {
	Host      string // e.g. "niedersachsen.onleihe.de"
	OnleiheID string
	LibraryID string
	Session   Session
	// Called whenever the session changes (login / refresh) so it can be persisted.
	OnSession func()

	http *http.Client
}

func NewClient(host string) *Client {
	return &Client{
		Host: normalizeHost(host),
		http: &http.Client{Timeout: 60 * time.Second},
	}
}

func normalizeHost(h string) string {
	h = strings.TrimSpace(h)
	h = strings.TrimPrefix(h, "https://")
	h = strings.TrimPrefix(h, "http://")
	if i := strings.IndexByte(h, '/'); i >= 0 {
		h = h[:i]
	}
	return strings.ToLower(h)
}

type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	msg := e.Body
	var m map[string]any
	if json.Unmarshal([]byte(e.Body), &m) == nil {
		if s, ok := m["message"].(string); ok && s != "" {
			msg = s
		}
	}
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return fmt.Sprintf("Onleihe API error %d: %s", e.Status, msg)
}

func (c *Client) do(method, path string, params url.Values, body any, auth bool) (any, error) {
	raw, err := c.doRaw(method, path, params, body, auth, true)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, nil
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("invalid JSON from %s: %w", path, err)
	}
	return out, nil
}

func (c *Client) doRaw(method, path string, params url.Values, body any, auth, retry bool) ([]byte, error) {
	// Renew the session shortly before the access token expires.
	if auth && retry && c.Session.AccessToken != "" && c.Session.ExpiresAt != 0 &&
		time.Now().Unix()+30 >= c.Session.ExpiresAt {
		if err := c.Refresh(); err != nil {
			log.Printf("proactive token refresh failed: %v", err)
		}
	}
	u := path
	if !strings.HasPrefix(path, "http") {
		u = apiBase + path
	}
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, u, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Origin", "https://"+c.Host)
	req.Header.Set("Referer", "https://"+c.Host+"/")
	req.Header.Set("User-Agent", "rm-onleihe/"+version)
	if auth && c.Session.AccessToken != "" && strings.HasPrefix(u, apiBase) {
		tt := c.Session.TokenType
		if tt == "" {
			tt = "Bearer"
		}
		req.Header.Set("Authorization", tt+" "+c.Session.AccessToken)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 50<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized && auth && c.Session.AccessToken != "" {
		if retry {
			if rerr := c.Refresh(); rerr == nil {
				return c.doRaw(method, path, params, body, auth, false)
			} else {
				log.Printf("token refresh after 401 failed: %v", rerr)
			}
		}
		c.expireSession()
		return nil, ErrSessionExpired
	}
	if resp.StatusCode >= 400 {
		return nil, &APIError{Status: resp.StatusCode, Body: string(data)}
	}
	return data, nil
}

// ---------------------------------------------------------------- libraries

type Library struct {
	OnleiheID   string `json:"onleiheId"`
	OnleiheName string `json:"onleiheName"`
	LibraryID   string `json:"libraryId"`
	LibraryName string `json:"libraryName"`
	City        string `json:"city"`
}

// Libraries returns the libraries reachable through the configured host.
// A single-library Onleihe returns one entry; a consortium (e.g.
// niedersachsen.onleihe.de) returns all member libraries.
func (c *Client) Libraries() ([]Library, error) {
	p, err := c.do("GET", "/management/v1/auth/domains", url.Values{"host": {c.Host}}, nil, false)
	if err != nil {
		return nil, err
	}
	var libs []Library
	var onleiheIDs []string
	// The endpoint returns a plain JSON array; older responses wrapped it in "content".
	domains := asList(get(p, "content"))
	if domains == nil {
		domains = asList(p)
	}
	for _, m := range domains {
		l := Library{
			OnleiheID:   str(get(m, "onleiheId")),
			OnleiheName: str(get(m, "onleiheName")),
			LibraryID:   str(get(m, "libraryId")),
			LibraryName: str(get(m, "libraryName")),
		}
		if l.OnleiheID != "" {
			onleiheIDs = append(onleiheIDs, l.OnleiheID)
		}
		if l.LibraryID != "" {
			libs = append(libs, l)
		}
	}
	if len(libs) > 0 || len(onleiheIDs) == 0 {
		if len(onleiheIDs) == 0 {
			return nil, fmt.Errorf("no Onleihe found for host %q", c.Host)
		}
		return libs, nil
	}
	// Host maps to an Onleihe without a fixed library: list its members.
	oid := onleiheIDs[0]
	for page := 1; page <= 20; page++ {
		lp, err := c.do("GET", "/user-application/v2/auth/libraries",
			url.Values{"page": {fmt.Sprint(page)}, "size": {"100"}, "onleiheIds": {oid}}, nil, false)
		if err != nil {
			return nil, err
		}
		content := asList(get(lp, "content"))
		if content == nil {
			content = asList(lp)
		}
		for _, m := range content {
			city := str(get(m, "city"))
			if city == "" {
				city = str(get(m, "address", "city"))
			}
			name := str(get(m, "name"))
			if name == "" {
				name = str(get(m, "libraryName"))
			}
			libs = append(libs, Library{OnleiheID: oid, LibraryID: str(get(m, "id")), LibraryName: name, City: city})
		}
		total := int(num(get(lp, "totalPages")))
		if len(content) == 0 || total == 0 || page >= total {
			break
		}
	}
	return libs, nil
}

// --------------------------------------------------------------------- auth

func (c *Client) Login(username, password string) error {
	if c.OnleiheID == "" || c.LibraryID == "" {
		return errors.New("library not selected")
	}
	p, err := c.do("POST", "/user-application/v1/auth/login", nil, map[string]any{
		"onleiheId": c.OnleiheID,
		"libraryId": c.LibraryID,
		"username":  username,
		"password":  password,
	}, false)
	if err != nil {
		return err
	}
	c.setSession(p, Session{})
	if c.Session.AccessToken == "" {
		return errors.New("login response contained no access token")
	}
	return nil
}

func (c *Client) Refresh() error {
	// Like the Onleihe web frontend, fall back to the access token when the
	// login response did not include a separate refresh token.
	tok := c.Session.RefreshToken
	if tok == "" {
		tok = c.Session.AccessToken
	}
	if tok == "" {
		return ErrAuth
	}
	p, err := c.do("POST", "/user-application/v1/auth/refresh", nil, map[string]any{"token": tok}, false)
	if err != nil {
		return err
	}
	c.setSession(p, c.Session)
	return nil
}

func (c *Client) Logout() {
	if c.Session.RefreshToken != "" {
		_, _ = c.do("POST", "/user-application/v1/auth/logout", nil,
			map[string]any{"refreshToken": c.Session.RefreshToken}, false)
	}
	c.Session = Session{}
	if c.OnSession != nil {
		c.OnSession()
	}
}

// expireSession drops the tokens but keeps the selected Onleihe and library,
// so logging in again only needs the password.
func (c *Client) expireSession() {
	c.Session = Session{}
	if c.OnSession != nil {
		c.OnSession()
	}
}

func (c *Client) setSession(p any, prev Session) {
	s := Session{
		AccessToken:  firstStr(get(p, "accessToken"), get(p, "access_token"), get(p, "token")),
		RefreshToken: firstStr(get(p, "refreshToken"), get(p, "refresh_token")),
		TokenType:    firstStr(get(p, "tokenType"), get(p, "token_type")),
	}
	claims := jwtClaims(s.AccessToken)
	s.UserID = firstStr(get(p, "userId"), get(p, "uid"), claims["uid"])
	s.ProfileID = firstStr(get(p, "profileId"), claims["pid"])
	if exp, ok := claims["exp"].(float64); ok {
		s.ExpiresAt = int64(exp)
	}
	if s.RefreshToken == "" {
		s.RefreshToken = prev.RefreshToken
	}
	if s.UserID == "" {
		s.UserID = prev.UserID
	}
	if s.ProfileID == "" {
		s.ProfileID = prev.ProfileID
	}
	if lid := firstStr(get(p, "libraryId"), claims["lid"]); lid != "" {
		c.LibraryID = lid
	}
	if oid := firstStr(get(p, "onleiheId"), claims["oid"]); oid != "" {
		c.OnleiheID = oid
	}
	c.Session = s
	if c.OnSession != nil {
		c.OnSession()
	}
}

func (c *Client) LoggedIn() bool { return c.Session.AccessToken != "" && c.Session.UserID != "" }

func (c *Client) profile() string {
	if c.Session.ProfileID != "" {
		return c.Session.ProfileID
	}
	return "master"
}

func jwtClaims(tok string) map[string]any {
	parts := strings.Split(tok, ".")
	if len(parts) < 2 {
		return map[string]any{}
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return map[string]any{}
	}
	return m
}

// -------------------------------------------------------------------- media

type Media struct {
	ProductID    string   `json:"productId"`
	Title        string   `json:"title"`
	Subtitle     string   `json:"subtitle,omitempty"`
	Authors      string   `json:"authors"`
	MediaType    string   `json:"mediaType"`
	LendID       string   `json:"lendId,omitempty"`
	Availability string   `json:"availability,omitempty"`
	Actions      []string `json:"actions,omitempty"`
	CanLend      bool     `json:"canLend"`
	DRM          string   `json:"drm"` // "free", "drm" or "" (unknown)
}

type SearchPage struct {
	Items []Media `json:"items"`
	Total int     `json:"total"`
	From  int     `json:"from"`
}

func (c *Client) Search(query string, from, size int) (*SearchPage, error) {
	if !c.LoggedIn() {
		return nil, ErrAuth
	}
	params := url.Values{"libraryId": {c.LibraryID}, "userId": {c.Session.UserID}}
	body := map[string]any{
		"from":         from,
		"size":         size,
		"userLanguage": "de_DE",
		"query": []map[string]any{{
			"query": query, "fields": []string{}, "isExact": false, "operator": "AND",
		}},
	}
	p, err := c.do("POST", "/ui/v1/onleihe/"+url.PathEscape(c.OnleiheID)+"/search", params, body, true)
	if err != nil {
		return nil, err
	}
	content := asList(get(p, "content"))
	if content == nil {
		content = asList(get(p, "items"))
	}
	if content == nil {
		content = asList(get(p, "results"))
	}
	page := &SearchPage{From: from, Total: int(num(firstAny(get(p, "totalItems"), get(p, "total"))))}
	for _, it := range content {
		page.Items = append(page.Items, parseMedia(it))
	}
	return page, nil
}

// MyMedia lists the user's current loans.
func (c *Client) MyMedia() ([]Media, error) {
	if !c.LoggedIn() {
		return nil, ErrAuth
	}
	p, err := c.do("GET", "/ui/v2/pages/my-media", url.Values{
		"onleiheId": {c.OnleiheID}, "userId": {c.Session.UserID},
		"profileId": {c.profile()}, "libraryId": {c.LibraryID},
	}, nil, true)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []Media
	walkMedia(p, func(m any) {
		md := parseMedia(m)
		if md.LendID == "" || seen[md.LendID] {
			return
		}
		seen[md.LendID] = true
		out = append(out, md)
	})
	return out, nil
}

func walkMedia(p any, fn func(any)) {
	switch v := p.(type) {
	case []any:
		for _, x := range v {
			walkMedia(x, fn)
		}
	case map[string]any:
		_, hasProduct := v["product"].(map[string]any)
		_, hasType := v["mediaType"]
		_, hasPID := v["productId"]
		if hasProduct || hasType || hasPID {
			fn(v)
		}
		for _, x := range v {
			walkMedia(x, fn)
		}
	}
}

func parseMedia(p any) Media {
	prod := get(p, "product")
	if _, ok := prod.(map[string]any); !ok {
		prod = p
	}
	var authors []string
	for _, a := range asList(get(prod, "authors")) {
		if m, ok := a.(map[string]any); ok {
			name := strings.TrimSpace(str(m["firstName"]) + " " + str(m["lastName"]))
			if name != "" {
				authors = append(authors, name)
			}
		} else if s := str(a); s != "" {
			authors = append(authors, s)
		}
	}
	m := Media{
		ProductID: firstStr(get(p, "productId"), get(prod, "id")),
		Title:     str(get(prod, "title")),
		Subtitle:  firstStr(get(prod, "subTitle"), get(prod, "subtitle")),
		Authors:   strings.Join(authors, ", "),
		MediaType: str(get(prod, "mediaType")),
		LendID:    str(get(p, "userInformation", "lend", "lendId")),
		DRM:       detectDRM(prod),
	}
	if m.DRM == drmUnknown {
		m.DRM = detectDRM(get(p, "userInformation"))
	}
	if m.DRM == drmUnknown && debug {
		if b, err := json.Marshal(p); err == nil {
			log.Printf("drm unknown for %q: %s", m.Title, b)
		}
	}
	for _, a := range asList(get(p, "userAction")) {
		s := str(a)
		if s == "" {
			s = firstStr(get(a, "type"), get(a, "action"), get(a, "name"))
		}
		if s != "" {
			m.Actions = append(m.Actions, s)
			if strings.Contains(strings.ToUpper(s), "LEND") {
				m.CanLend = true
			}
		}
	}
	if av, ok := get(p, "status", "availabilityInformation").(map[string]any); ok {
		var parts []string
		for k, v := range av {
			switch v.(type) {
			case string, float64, bool:
				parts = append(parts, fmt.Sprintf("%s: %v", k, v))
			}
		}
		m.Availability = strings.Join(parts, ", ")
	}
	return m
}

// --------------------------------------------------------------------- lend

func (c *Client) Lend(productID string, days int) (string, error) {
	if !c.LoggedIn() {
		return "", ErrAuth
	}
	body := map[string]any{"productId": productID}
	if days > 0 {
		body["lendingDuration"] = days * 24 * 60 * 60
	}
	path := fmt.Sprintf("/ui/v2/onleihe/%s/library/%s/users/%s/profiles/%s/lends",
		url.PathEscape(c.OnleiheID), url.PathEscape(c.LibraryID),
		url.PathEscape(c.Session.UserID), url.PathEscape(c.profile()))
	p, err := c.do("POST", path, nil, body, true)
	if err != nil {
		return "", err
	}
	if lid := lendIDFrom(p); lid != "" {
		return lid, nil
	}
	// Lending may be processed asynchronously as a job.
	jobID := str(get(p, "jobId"))
	if jobID == "" && (get(p, "state") != nil || strings.Contains(strings.ToUpper(str(get(p, "type"))), "JOB")) {
		jobID = str(get(p, "id"))
	}
	if jobID != "" {
		if err := c.waitJob(jobID, 45*time.Second); err != nil {
			return "", err
		}
	}
	// Find the new loan in "my media".
	items, err := c.MyMedia()
	if err != nil {
		return "", err
	}
	for _, it := range items {
		if it.ProductID == productID {
			return it.LendID, nil
		}
	}
	return "", errors.New("title was lent, but the loan was not found in your media yet; check “My loans”")
}

func lendIDFrom(p any) string {
	return firstStr(get(p, "lendId"), get(p, "lend", "lendId"), get(p, "userInformation", "lend", "lendId"))
}

func (c *Client) waitJob(jobID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	path := fmt.Sprintf("/ui/v1/users/%s/profiles/%s/jobs/%s",
		url.PathEscape(c.Session.UserID), url.PathEscape(c.profile()), url.PathEscape(jobID))
	for time.Now().Before(deadline) {
		p, err := c.do("GET", path, nil, nil, true)
		if err != nil {
			return err
		}
		if e, ok := get(p, "apiError").(map[string]any); ok && len(e) > 0 {
			return fmt.Errorf("lending failed: %s", firstStr(e["message"], "unknown error"))
		}
		state := strings.ToUpper(str(get(p, "state")))
		switch state {
		case "DONE":
			return nil
		case "FAILED", "ERROR":
			return fmt.Errorf("lending job %s", strings.ToLower(state))
		}
		if b, ok := get(p, "completed").(bool); ok && b {
			return nil
		}
		time.Sleep(time.Second)
	}
	return errors.New("timed out waiting for the lending job")
}

// ----------------------------------------------------------------- download

type Licence struct {
	ACSMURL string // Adobe ACS licence token (DRM)
	EPUBURL string
	PDFURL  string
	Raw     any
}

func (c *Client) PlayerLicence(lendID string) (*Licence, error) {
	p, err := c.do("GET", "/drm-facade/v1/drm/lend/"+url.PathEscape(lendID)+"/player-licence", nil, nil, true)
	if err != nil {
		return nil, err
	}
	l := &Licence{Raw: p}
	l.ACSMURL = findNamedURL(p, "licenceUrl", "licenseUrl", "acsmUrl", "acsmURL")
	if l.ACSMURL == "" {
		l.ACSMURL = findDRMURI(p, "ACS")
	}
	l.EPUBURL = findMimeURI(p, "EPUB")
	l.PDFURL = findMimeURI(p, "PDF")
	return l, nil
}

// Fetch downloads a URL (authenticated if it points at the API).
func (c *Client) Fetch(u string) ([]byte, string, error) {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "rm-onleihe/"+version)
	req.Header.Set("Referer", "https://"+c.Host+"/")
	if strings.HasPrefix(u, apiBase) && c.Session.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.Session.AccessToken)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, "", &APIError{Status: resp.StatusCode, Body: string(b)}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 500<<20))
	return data, resp.Header.Get("Content-Type"), err
}

func findNamedURL(p any, keys ...string) string {
	switch v := p.(type) {
	case map[string]any:
		for _, k := range keys {
			if s := str(v[k]); isHTTP(s) {
				return s
			}
		}
		for _, x := range v {
			if s := findNamedURL(x, keys...); s != "" {
				return s
			}
		}
	case []any:
		for _, x := range v {
			if s := findNamedURL(x, keys...); s != "" {
				return s
			}
		}
	}
	return ""
}

func findDRMURI(p any, system string) string {
	switch v := p.(type) {
	case map[string]any:
		sys := strings.ToLower(str(v["drmSystem"]))
		cls := strings.ToLower(str(v["_class"]))
		if sys == strings.ToLower(system) || strings.HasSuffix(cls, strings.ToLower(system)+"drminformation") {
			if s := str(v["uri"]); isHTTP(s) {
				return s
			}
		}
		for _, x := range v {
			if s := findDRMURI(x, system); s != "" {
				return s
			}
		}
	case []any:
		for _, x := range v {
			if s := findDRMURI(x, system); s != "" {
				return s
			}
		}
	}
	return ""
}

func findMimeURI(p any, mime string) string {
	switch v := p.(type) {
	case map[string]any:
		if strings.EqualFold(str(v["mimeType"]), mime) {
			if s := firstStr(v["uri"], v["url"]); isHTTP(s) {
				return s
			}
		}
		for _, x := range v {
			if s := findMimeURI(x, mime); s != "" {
				return s
			}
		}
	case []any:
		for _, x := range v {
			if s := findMimeURI(x, mime); s != "" {
				return s
			}
		}
	}
	return ""
}

// ------------------------------------------------------------ JSON helpers

func get(p any, path ...string) any {
	cur := p
	for _, k := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[k]
	}
	return cur
}

func asList(p any) []any {
	l, _ := p.([]any)
	return l
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprint(x)
	case json.Number:
		return x.String()
	}
	return ""
}

func num(v any) float64 {
	f, _ := v.(float64)
	return f
}

func firstStr(vs ...any) string {
	for _, v := range vs {
		if s := str(v); s != "" {
			return s
		}
	}
	return ""
}

func firstAny(vs ...any) any {
	for _, v := range vs {
		if v != nil {
			return v
		}
	}
	return nil
}

func isHTTP(s string) bool {
	return strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")
}
