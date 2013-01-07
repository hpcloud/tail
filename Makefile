default:	test

test:	*.go
	go test -v

fmt:
	go fmt .
