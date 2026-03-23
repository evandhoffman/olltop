.PHONY: build clean run test

VERSION ?= dev
BINARY := olltop
CMD := ./cmd/olltop
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

build:
	CGO_ENABLED=1 go build $(LDFLAGS) -o $(BINARY) $(CMD)

clean:
	rm -f $(BINARY) $(BINARY)-darwin-*

run: build
	sudo ./$(BINARY)

test:
	go test ./...
