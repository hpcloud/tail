default:	test

test:	*.go
	go test -v -race ./...

fmt:
	gofmt -w .

mod:
	go mod tidy

vendor:
	go mod vendor
# Run the test in an isolated environment.
fulltest:
	docker build -t hpcloud/tail -f Dockerfile-go-1.9 .
	docker build -t hpcloud/tail .

all: fmt mod vendor test
