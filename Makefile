BINARY := moqsub
BIN_DIR := bin
OUTPUT := $(BIN_DIR)/$(BINARY)

.PHONY: all build clean

all: build

build: 
	mkdir -p $(BIN_DIR)
	go build -o $(OUTPUT) ./cmd/moqsub

clean:
	rm -rf $(BIN_DIR)
