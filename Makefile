build:
	go build -ldflags="-s -w" -o vps .

linux:
	GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o vps-linux-amd64 .
	GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o vps-linux-arm64 .

clean:
	rm -f vps vps-linux-*

.PHONY: build linux clean
