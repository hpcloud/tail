default:	test

test:	*.go
	go test -v -race -timeout 30s ./...

fmt:
	gofmt -w .

# Run the test in an isolated environment.
fulltest:
	docker build -t hpcloud/tail .
