BINARY := wltts
PKG_CONFIG_PATH := $(CURDIR)/build/pkgconfig:$(PKG_CONFIG_PATH)
export PKG_CONFIG_PATH

.PHONY: all build run server test vet tidy clean

all: build

build:
	go build -o $(BINARY) .

run: build
	./$(BINARY)

server: build
	./$(BINARY) -server

test:
	go test ./...

vet:
	go vet ./...

tidy:
	go mod tidy

clean:
	rm -f $(BINARY)
