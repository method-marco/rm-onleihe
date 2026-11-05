# Build the AppLoad app for reMarkable.
#
#   make            -> dist/rm-onleihe-aarch64.tar.gz (Paper Pro / Move)
#                      dist/rm-onleihe-armv7.tar.gz   (reMarkable 1 / 2)
#   make docker     -> same, inside a container (needs only Docker)
#   make test       -> run backend unit tests
#   make install HOST=10.11.99.1 ARCH=aarch64 -> copy to a device over SSH

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev)
RCC     ?= rcc
APP      = rm-onleihe
GOFLAGS  = -trimpath -ldflags "-s -w -X main.version=$(VERSION)"
HOST    ?= 10.11.99.1
ARCH    ?= aarch64
DEST     = /home/root/xovi/exthome/appload/$(APP)

.PHONY: all test clean docker install aarch64 armv7

all: aarch64 armv7

build/resources.rcc: app/application.qrc app/ui/*.qml
	mkdir -p build
	$(RCC) --binary -o $@ app/application.qrc

build/%/backend/entry: backend/*.go backend/go.mod
	mkdir -p $(dir $@)
	cd backend && CGO_ENABLED=0 GOOS=linux \
		$(if $(filter aarch64,$*),GOARCH=arm64,GOARCH=arm GOARM=7) \
		go build $(GOFLAGS) -o ../$@ .

aarch64 armv7: %: build/resources.rcc build/%/backend/entry
	rm -rf build/$*/$(APP) && mkdir -p build/$*/$(APP)/backend dist
	cp app/manifest.json app/icon.png build/resources.rcc build/$*/$(APP)/
	cp build/$*/backend/entry build/$*/$(APP)/backend/entry
	chmod 755 build/$*/$(APP)/backend/entry
	cp LICENSE build/$*/$(APP)/
	tar -C build/$* -czf dist/$(APP)-$*.tar.gz $(APP)

test:
	cd backend && go vet ./... && go test ./...

docker:
	docker run --rm -v "$(CURDIR)":/src -w /src -e VERSION=$(VERSION) golang:1.22-bookworm \
		sh -c 'apt-get update -qq && apt-get install -y -qq qt6-base-dev-tools >/dev/null && \
		       make all test RCC=/usr/lib/qt6/libexec/rcc'

install: $(ARCH)
	ssh root@$(HOST) 'mkdir -p /home/root/xovi/exthome/appload'
	cat dist/$(APP)-$(ARCH).tar.gz | ssh root@$(HOST) 'tar -C /home/root/xovi/exthome/appload -xzf -'

clean:
	rm -rf build dist
