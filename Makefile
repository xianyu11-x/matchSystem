.PHONY: all build run test bench-match clean

APP_NAME = matchSystem.exe
MAIN_FILE = cmd/app/main.go

all: build

build:
	go build -o bin/$(APP_NAME) $(MAIN_FILE)

run: build
	.\bin\$(APP_NAME)

test:
	go test ./...

bench-match:
	go run ./cmd/match-benchmark

clean:
	if exist bin rmdir /s /q bin
