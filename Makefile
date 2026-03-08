.PHONY: build install clean

build:
	go build -o shinobi ./cmd/shinobi

install:
	go install ./cmd/shinobi

clean:
	rm -f shinobi
