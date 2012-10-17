default:	test

setup:
	GOPATH=`pwd`/.go go get -d -v .
	GOPATH=`pwd`/.go go test -v -i
	rm -f `pwd`/.go/src/tail
	ln -sf `pwd` `pwd`/.go/src/tail

test:	*.go
	GOPATH=`pwd`/.go go test -v

fmt:
	go fmt .
