BINARY   := flexy
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -ldflags "-X main.Version=$(VERSION)"

.PHONY: all linux-amd64 linux-arm64 linux-arm install clean

all:
	go build $(LDFLAGS) -o $(BINARY) .

linux-amd64:
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY)-linux-amd64 .

linux-arm64:
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BINARY)-linux-arm64 .

linux-arm:
	GOOS=linux GOARCH=arm GOARM=7 go build $(LDFLAGS) -o $(BINARY)-linux-armv7 .

# Usage: make install HOST=user@192.168.1.x
# Copies the matching linux binary and systemd unit, then enables the service.
install: linux-amd64
	@test -n "$(HOST)" || (echo "usage: make install HOST=user@host"; exit 1)
	ssh $(HOST) "mkdir -p /usr/local/bin"
	scp $(BINARY)-linux-amd64 $(HOST):/usr/local/bin/$(BINARY)
	scp flexy.service $(HOST):/etc/systemd/system/flexy.service
	ssh $(HOST) "systemctl daemon-reload && systemctl enable flexy && systemctl restart flexy"

install-arm64: linux-arm64
	@test -n "$(HOST)" || (echo "usage: make install-arm64 HOST=user@host"; exit 1)
	ssh $(HOST) "mkdir -p /usr/local/bin"
	scp $(BINARY)-linux-arm64 $(HOST):/usr/local/bin/$(BINARY)
	scp flexy.service $(HOST):/etc/systemd/system/flexy.service
	ssh $(HOST) "systemctl daemon-reload && systemctl enable flexy && systemctl restart flexy"

clean:
	rm -f $(BINARY) $(BINARY)-linux-*
