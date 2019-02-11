FROM golang:1.11-alpine

RUN apk add --no-cache git curl gcc musl-dev

WORKDIR /src/tail
COPY . .

# expecting to run the test successfully.
RUN go test -v .

RUN CGO_ENABLED=0 go build -installsuffix 'static' \
    -o ./bin/gotail ./cmd/gotail

RUN ./bin/gotail -h || true
