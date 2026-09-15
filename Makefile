.PHONY: build test install

build:
	go build -o envkit .

test:
	go test ./...
	go vet ./...
	./test.sh

install: build
	mkdir -p ~/.local/bin
	cp envkit ~/.local/bin/envkit
