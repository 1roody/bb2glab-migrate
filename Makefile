BIN := bb2gl
PKG := ./cmd/bb2gl
LDFLAGS := -s -w

.PHONY: build test vet dist clean

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) $(PKG)

test:
	go test ./...

vet:
	gofmt -l . | (! grep .) && go vet ./...

dist:
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BIN)-linux-amd64 $(PKG)
	GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BIN)-darwin-arm64 $(PKG)
	GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BIN)-darwin-amd64 $(PKG)

clean:
	rm -rf $(BIN) dist
