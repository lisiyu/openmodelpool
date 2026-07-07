.PHONY: build run clean test

BINARY=modelmux

build:
	go build -o $(BINARY) .

run: build
	./$(BINARY)

clean:
	rm -f $(BINARY)

test:
	go test -v ./...

docker:
	docker build -t modelmux .

fmt:
	go fmt ./...
	goimports -w .

lint:
	golangci-lint run
