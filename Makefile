default:	test

test:	*.go
	go test -v

fmt:
	go fmt .

fulltest:
	sudo docker build -t ActiveState/tail .
