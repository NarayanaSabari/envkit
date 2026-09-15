.PHONY: build test install

build:
	go build -o envkit .

test:
	go test ./...
	go vet ./...
	./test.sh

install: build
	mkdir -p ~/.local/bin
	rm -f ~/.local/bin/envkit
	cp envkit ~/.local/bin/envkit
