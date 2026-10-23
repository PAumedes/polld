BINARY   := polld
PREFIX   := $(HOME)/.local
UNITDIR  := $(HOME)/.config/systemd/user
CONF_DIR := $(HOME)/.config/polld

.PHONY: all build test cover lint vet vuln fmt clean install-user uninstall-user run-example

all: fmt vet test build

build:
	go build -o bin/$(BINARY) ./cmd/polld

test:
	go test -race -count=1 ./...

cover:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

lint:
	golangci-lint run ./...

vet:
	go vet ./...

vuln:
	govulncheck ./...

fmt:
	gofmt -w -s .
	go mod tidy

clean:
	rm -rf bin coverage.out

# Installs the binary, the user unit, and (if absent) the example config.
# Does not start the service — run: systemctl --user enable --now polld
install-user: build
	install -Dm755 bin/$(BINARY) $(DESTDIR)$(PREFIX)/bin/$(BINARY)
	install -Dm644 deploy/polld.service $(DESTDIR)$(UNITDIR)/polld.service
	@if [ ! -f $(CONF_DIR)/config.yaml ]; then \
		install -Dm600 configs/config.example.yaml $(CONF_DIR)/config.yaml; \
		echo "Created $(CONF_DIR)/config.yaml — edit it before starting"; \
	fi
	systemctl --user daemon-reload || true
	@echo "Enable with: systemctl --user enable --now polld"

uninstall-user:
	-systemctl --user disable --now polld
	rm -f $(PREFIX)/bin/$(BINARY) $(UNITDIR)/polld.service
	systemctl --user daemon-reload || true
	@echo "State and config left in place under $(CONF_DIR) and ~/.local/state/polld"
