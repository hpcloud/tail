# -*- sh -*-

FROM ubuntu:quantal

RUN echo "deb http://archive.ubuntu.com/ubuntu quantal main universe" >> /etc/apt/sources.list

RUN apt-get -qy update
RUN apt-get -qy install golang-go
RUN apt-get -qy install git mercurial bzr subversion

ENV GOPATH $HOME/go

RUN mkdir -p $GOPATH/src/github.com/ActiveState/tail/
ADD . $GOPATH/src/github.com/ActiveState/tail/

# expecting to fetch dependencies successfully.
RUN go get -v github.com/ActiveState/tail

# expecting to run the test successfully.
RUN go test -v github.com/ActiveState/tail

# expecting to install successfully
RUN go install -v github.com/ActiveState/tail 
RUN go install -v github.com/ActiveState/tail/cmd/gotail

RUN $GOPATH/bin/gotail -h || true

ENV PATH $GOPATH/bin:$PATH
CMD ["gotail"]

