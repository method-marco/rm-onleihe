# rm-onleihe

An [AppLoad](https://github.com/asivery/rm-appload) app for reMarkable tablets that lets you
**search, borrow and download e-books from your library's [Onleihe](https://www.onleihe.de)**
and then read them with [KOReader](https://koreader.rocks).

> **Status: early / experimental.** The Onleihe API is not publicly documented. This app uses
> the same JSON API as the current Onleihe web frontend (`api.onleihe.de`), following the
> endpoints used by [Onleiharr](https://github.com/nzb-tuxxx/Onleiharr). Expect breakage if
> Onleihe changes things. Bug reports with the error text are welcome.

## Important: DRM

Onleihe delivers titles in two ways:

| Delivery | What rm-onleihe does | Readable in KOReader? |
|---|---|---|
| DRM-free / watermarked EPUB or PDF (e.g. many e-papers, some publishers) | Saved to the download folder | ✅ yes |
| Adobe DRM (`.acsm` licence) | `.acsm` saved to `~/.config/rm-onleihe/acsm/`, with a message in the app | ❌ no |

KOReader cannot open Adobe-DRM-protected books. **This project does not remove or bypass DRM
and will not accept contributions that do.** For Adobe-DRM titles, use the `.acsm` file with
Adobe Digital Editions or another Adobe-licensed reader.

## Features

- Pick your Onleihe (e.g. `niedersachsen.onleihe.de`) and library, and log in with your library card
- Search the catalogue and borrow titles (optional custom lending period)
- Each search hit is marked "DRM-frei", "Adobe-DRM" or "DRM: unbekannt"; the "DRM ausblenden"
  toggle hides titles known to have Adobe DRM. Detection is best effort from catalogue metadata;
  titles with unknown status are always shown. Run with `RM_ONLEIHE_DEBUG=1` to log the raw
  metadata of titles whose status could not be determined.
- List your current loans and (re-)download them
- DRM-free files go straight into a folder you open in KOReader (default `/home/root/onleihe`)
- E-ink-friendly UI (black/white, no animations, large touch targets)
- Your password is **not** stored. Only the session token is kept, in
  `/home/root/.config/rm-onleihe/config.json` (mode 0600)

Note: Onleihe's terms of use forbid *automated* lending programs. rm-onleihe only borrows
when you tap "Ausleihen". Please return titles you no longer need on time.

## Requirements

- reMarkable 1, 2, Paper Pro or Paper Pro Move with [xovi](https://github.com/asivery/xovi) +
  [AppLoad](https://github.com/asivery/rm-appload)
- KOReader (e.g. `vellum add koreader`)

## Install

### With Vellum

```sh
vellum add onleihe
```

(once the package has been accepted into [vellum-dev/vellum](https://github.com/vellum-dev/vellum),
see [Publishing to Vellum](#publishing-to-vellum)).

### Manually

Download `rm-onleihe-aarch64.tar.gz` (Paper Pro / Move) or `rm-onleihe-armv7.tar.gz`
(reMarkable 1/2) from the [releases](../../releases) and extract it on the device:

```sh
scp rm-onleihe-aarch64.tar.gz root@10.11.99.1:
ssh root@10.11.99.1 'tar -C /home/root/xovi/exthome/appload -xzf rm-onleihe-aarch64.tar.gz'
```

Then reload AppLoad. The app shows up as **Onleihe**.

## Usage

1. Enter your Onleihe address and tap **Bibliothek suchen**. Select your library if the
   address is shared by several libraries.
2. Log in with your library card number and password.
3. Search for a title and tap **Ausleihen**. The book is borrowed and downloaded right away.
4. In KOReader, open the download folder (default `/home/root/onleihe`).
   Tip: set it as KOReader's home folder.

## Building

Needs Go ≥ 1.22 and Qt 6's `rcc`, or just Docker:

```sh
make docker            # builds dist/*.tar.gz and runs the tests in a container
make test              # backend unit tests (Go needed)
make all RCC=/path/to/rcc
make install ARCH=aarch64 HOST=10.11.99.1   # build and copy to a device over SSH
```

## Layout

```
app/
  manifest.json        AppLoad manifest (id "rm-onleihe", loadsBackend)
  application.qrc      compiled to resources.rcc
  ui/main.qml          frontend
  icon.png
backend/               Go backend → backend/entry (static, no cgo)
  appload.go           AppLoad socket protocol (SOCK_SEQPACKET, {int32 type, int32 len} + UTF-8)
  onleihe.go           Onleihe API client
  download.go          download + DRM detection (no circumvention)
  main.go              message handling, config
packaging/vellum/onleihe/VELBUILD
.github/workflows/release.yml   builds on every push, publishes release tarballs on tags v*
```

### Frontend ↔ backend messages

JSON bodies. Frontend → backend: `1` init, `2` set host, `3` login, `4` search, `5` lend,
`6` list loans, `7` download, `8` logout, `9` settings.
Backend → frontend: `101` state, `102` error, `103` search results, `104` loans,
`105` download result, `106` busy indicator, `107` library list, `108` lent.

## Releasing

```sh
git tag v0.1.0 && git push origin v0.1.0
```

GitHub Actions builds both architectures and attaches `rm-onleihe-aarch64.tar.gz`,
`rm-onleihe-armv7.tar.gz` and `SHA512SUMS` to the release.

## Publishing to Vellum

Vellum packages live in `packages/<name>/VELBUILD` of
[vellum-dev/vellum](https://github.com/vellum-dev/vellum).

1. Create a release (see above).
2. Fork vellum, copy `packaging/vellum/onleihe` to `packages/onleihe`, and fill in
   `maintainer`, `upstream_author` (your GitHub user) and `pkgver`.
3. `./scripts/update-checksums.sh onleihe`
4. `./scripts/lint-packages.sh onleihe --apkbuild-lint`
5. `./scripts/build-package.sh onleihe aarch64`
6. Open the PR **yourself**. Vellum's rules say PRs must be opened by a person, the
   description must not be LLM-written, and commits must not carry `Co-Authored-By`
   trailers naming an AI assistant.
7. After maintainers publish it to testing, test on device
   (`vellum testing enable && vellum update && vellum add onleihe@testing`) and comment
   `/ready-for-review` on the PR.

## License

MIT, see [LICENSE](LICENSE). Not affiliated with divibib GmbH / Onleihe.
