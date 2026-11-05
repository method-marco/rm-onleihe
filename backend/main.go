// rm-onleihe backend: talks to the AppLoad frontend over the AppLoad socket
// and to Onleihe over HTTPS.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var version = "dev"

// debug enables extra logging (e.g. raw product JSON when DRM status is unknown).
var debug = os.Getenv("RM_ONLEIHE_DEBUG") != ""

// Message types frontend -> backend.
const (
	reqInit     = 1
	reqSetHost  = 2
	reqLogin    = 3
	reqSearch   = 4
	reqLend     = 5
	reqLoans    = 6
	reqDownload = 7
	reqLogout   = 8
	reqSettings = 9
)

// Message types backend -> frontend.
const (
	resState     = 101
	resError     = 102
	resSearch    = 103
	resLoans     = 104
	resDownload  = 105
	resBusy      = 106
	resLibraries = 107
	resLent      = 108
)

type Config struct {
	Host        string  `json:"host"`
	OnleiheID   string  `json:"onleiheId"`
	LibraryID   string  `json:"libraryId"`
	LibraryName string  `json:"libraryName"`
	Username    string  `json:"username"`
	Session     Session `json:"session"`
	DownloadDir string  `json:"downloadDir"`
	LendDays    int     `json:"lendDays"` // 0 = library default
	HideDRM     bool    `json:"hideDrm"`  // hide titles known to have Adobe DRM in search
}

type App struct {
	al      *AppLoad
	mu      sync.Mutex // serialises Onleihe operations
	cfg     Config
	cfgPath string
	client  *Client
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: entry <appload-socket>")
		os.Exit(2)
	}
	al, err := ConnectAppLoad(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	defer al.Close()

	app := &App{al: al, cfgPath: configPath()}
	app.loadConfig()

	for {
		typ, contents, err := al.Receive()
		if err != nil {
			log.Printf("receive: %v", err)
			return
		}
		switch typ {
		case msgSystemTerminate:
			return
		case msgSystemNewCoordinator:
			app.sendState()
		case msgSystemLostCoordinator:
			// a frontend went away; keep running
		default:
			go app.handle(typ, contents)
		}
	}
}

func configPath() string {
	if p := os.Getenv("RM_ONLEIHE_CONFIG"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "/home/root"
	}
	return filepath.Join(home, ".config", "rm-onleihe", "config.json")
}

func defaultDownloadDir() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "/home/root"
	}
	return filepath.Join(home, "onleihe")
}

func (a *App) loadConfig() {
	b, err := os.ReadFile(a.cfgPath)
	if err == nil {
		_ = json.Unmarshal(b, &a.cfg)
	}
	if a.cfg.DownloadDir == "" {
		a.cfg.DownloadDir = defaultDownloadDir()
	}
	a.newClient()
}

func (a *App) newClient() {
	c := NewClient(a.cfg.Host)
	c.OnleiheID = a.cfg.OnleiheID
	c.LibraryID = a.cfg.LibraryID
	c.Session = a.cfg.Session
	c.OnSession = func() {
		a.cfg.Session = c.Session
		a.cfg.OnleiheID = c.OnleiheID
		a.cfg.LibraryID = c.LibraryID
		a.saveConfig()
	}
	a.client = c
}

// saveConfig stores the config (including the refresh token, never the
// password) with 0600 permissions.
func (a *App) saveConfig() {
	if err := os.MkdirAll(filepath.Dir(a.cfgPath), 0o700); err != nil {
		log.Printf("config dir: %v", err)
		return
	}
	b, _ := json.MarshalIndent(a.cfg, "", "  ")
	tmp := a.cfgPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		log.Printf("write config: %v", err)
		return
	}
	_ = os.Rename(tmp, a.cfgPath)
}

func (a *App) send(typ int32, v any) {
	b, _ := json.Marshal(v)
	if err := a.al.Send(typ, string(b)); err != nil {
		log.Printf("send: %v", err)
	}
}

func (a *App) fail(err error) {
	msg := err.Error()
	switch {
	case errors.Is(err, ErrSessionExpired):
		msg = "Sitzung abgelaufen. Bitte Passwort erneut eingeben."
		defer a.sendState() // switch the UI to the (pre-filled) login screen
	case errors.Is(err, ErrAuth):
		msg = "Bitte zuerst anmelden. / Please log in first."
	}
	a.send(resError, map[string]string{"message": msg})
}

func (a *App) busy(text string) { a.send(resBusy, map[string]any{"busy": text != "", "text": text}) }

func (a *App) sendState() {
	a.send(resState, map[string]any{
		"version":     version,
		"loggedIn":    a.client.LoggedIn(),
		"host":        a.cfg.Host,
		"onleiheId":   a.cfg.OnleiheID,
		"libraryId":   a.cfg.LibraryID,
		"libraryName": a.cfg.LibraryName,
		"username":    a.cfg.Username,
		"downloadDir": a.cfg.DownloadDir,
		"lendDays":    a.cfg.LendDays,
		"hideDrm":     a.cfg.HideDRM,
	})
}

func (a *App) handle(typ int32, contents string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	defer a.busy("")

	var req map[string]any
	if contents != "" {
		_ = json.Unmarshal([]byte(contents), &req)
	}

	switch typ {
	case reqInit:
		a.sendState()

	case reqSetHost:
		host := normalizeHost(str(req["host"]))
		if host == "" {
			a.fail(fmt.Errorf("bitte eine Onleihe-Adresse eingeben, z. B. niedersachsen.onleihe.de"))
			return
		}
		a.busy("Bibliotheken werden geladen …")
		c := NewClient(host)
		libs, err := c.Libraries()
		if err != nil {
			a.fail(err)
			return
		}
		a.send(resLibraries, map[string]any{"host": host, "items": libs})

	case reqLogin:
		a.busy("Anmeldung …")
		a.cfg.Host = normalizeHost(str(req["host"]))
		a.cfg.OnleiheID = str(req["onleiheId"])
		a.cfg.LibraryID = str(req["libraryId"])
		a.cfg.LibraryName = str(req["libraryName"])
		a.cfg.Username = str(req["username"])
		a.cfg.Session = Session{}
		a.newClient()
		if err := a.client.Login(a.cfg.Username, str(req["password"])); err != nil {
			a.fail(err)
			a.sendState()
			return
		}
		a.saveConfig()
		a.sendState()

	case reqLogout:
		a.client.Logout()
		a.saveConfig()
		a.sendState()

	case reqSettings:
		if d := strings.TrimSpace(str(req["downloadDir"])); d != "" {
			a.cfg.DownloadDir = d
		}
		if v, ok := req["lendDays"].(float64); ok && v >= 0 {
			a.cfg.LendDays = int(v)
		}
		if v, ok := req["hideDrm"].(bool); ok {
			a.cfg.HideDRM = v
		}
		a.saveConfig()
		a.sendState()

	case reqSearch:
		q := strings.TrimSpace(str(req["query"]))
		from := int(num(req["from"]))
		a.busy("Suche …")
		page, err := a.client.Search(q, from, 20)
		if err != nil {
			a.fail(err)
			return
		}
		a.send(resSearch, map[string]any{"query": q, "from": page.From, "total": page.Total, "items": page.Items})

	case reqLoans:
		a.busy("Ausleihen werden geladen …")
		items, err := a.client.MyMedia()
		if err != nil {
			a.fail(err)
			return
		}
		a.send(resLoans, map[string]any{"items": items})

	case reqLend:
		pid := str(req["productId"])
		a.busy("Titel wird ausgeliehen …")
		lendID, err := a.client.Lend(pid, a.cfg.LendDays)
		if err != nil {
			a.fail(err)
			return
		}
		a.send(resLent, map[string]any{"productId": pid, "lendId": lendID})
		a.download(lendID, str(req["title"]), str(req["authors"]))

	case reqDownload:
		a.download(str(req["lendId"]), str(req["title"]), str(req["authors"]))

	default:
		log.Printf("unknown message type %d", typ)
	}
}

func (a *App) download(lendID, title, authors string) {
	if lendID == "" {
		a.fail(fmt.Errorf("keine Ausleihe-ID"))
		return
	}
	a.busy("Download …")
	res, err := Download(a.client, lendID, a.cfg.DownloadDir, filepath.Join(filepath.Dir(a.cfgPath), "acsm"), title, authors)
	if err != nil {
		a.fail(err)
		return
	}
	a.send(resDownload, res)
}
