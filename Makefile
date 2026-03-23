.PHONY: build clean run test

BINARY := olltop
CMD := ./cmd/olltop

build:
	CGO_ENABLED=1 go build -o $(BINARY) $(CMD)

clean:
	rm -f $(BINARY)

run: build
	sudo ./$(BINARY)

test:
	go test ./...
