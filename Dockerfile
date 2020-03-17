FROM golang

RUN mkdir -p $GOPATH/src/github.com/hpcloud/tail/
ADD . /src/tail/
WORKDIR /src/tail

# expecting to run the test successfully.
RUN go test -race -v ./...

# expecting to install successfully
RUN go install -v .
RUN go install -v ./cmd/gotail

RUN $GOPATH/bin/gotail -h || true

ENV PATH $GOPATH/bin:$PATH
CMD ["gotail"]
