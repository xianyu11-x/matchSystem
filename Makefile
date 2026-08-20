.PHONY: all build run test clean

APP_NAME = matchSystem.exe
MAIN_FILE = cmd/app/main.go

all: build

build:
	go build -o bin/$(APP_NAME) $(MAIN_FILE)

run: build
	.\bin\$(APP_NAME)

test:
	go test ./...

clean:
	if exist bin rmdir /s /q bin
