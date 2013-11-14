default:	test

test:	*.go
	go test -v

fmt:
	gofmt -w .

# Run the test in an isolated environment.
fulltest:
	sudo docker build -t ActiveState/tail .
