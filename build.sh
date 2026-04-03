#!/bin/bash

set -euo pipefail

# This script compiles the Go applications and prepares them for distribution.

APP_DIR="./app"
MAIN_BINARY="puckerup"
PASSWORD_BINARY="puckerup-passwd"
STATIC_FILES=("index.html" "login.html" "dashboard.html")

echo "--- Starting SpeedUP Build ---"

if ! command -v go >/dev/null 2>&1; then
	echo "Error: Go is not installed or not available in PATH."
	exit 1
fi

for file in "${STATIC_FILES[@]}" "main.go" "generate-password.go"; do
	if [ ! -f "$file" ]; then
		echo "Error: Required source file '$file' was not found."
		exit 1
	fi
done

# Set the Go build flags to create a static binary for Linux.
# This makes it highly portable across different Linux distributions.
export CGO_ENABLED=0
export GOOS=linux
export GOARCH=amd64

echo "Creating distribution directory..."
rm -rf "$APP_DIR"
mkdir -p "$APP_DIR"

echo "Compiling main server..."
go build -o "$APP_DIR/$MAIN_BINARY" ./main.go

echo "Compiling password generator..."
go build -o "$APP_DIR/$PASSWORD_BINARY" ./generate-password.go

echo "Copying web files..."
for file in "${STATIC_FILES[@]}"; do
	cp "./$file" "$APP_DIR/$file"
done

if [ ! -s "$APP_DIR/$MAIN_BINARY" ] || [ ! -s "$APP_DIR/$PASSWORD_BINARY" ]; then
	echo "Error: One or more binaries were not produced correctly."
	exit 1
fi

for file in "${STATIC_FILES[@]}"; do
	if [ ! -s "$APP_DIR/$file" ]; then
		echo "Error: Required runtime file '$file' is missing from $APP_DIR."
		exit 1
	fi
done

echo "--- Build Complete ---"
echo "Distribution files are ready in the 'app' directory."

printf '%s\n' \
	"$APP_DIR/index.html" \
	"$APP_DIR/login.html" \
	"$APP_DIR/dashboard.html" \
	"$APP_DIR/$MAIN_BINARY" \
	"$APP_DIR/$PASSWORD_BINARY"
