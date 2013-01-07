# Tail implementation in Go

A Go package striving to emulate the BSD `tail` program. 

```Go
t := tail.TailFile("/var/log/nginx.log", tail.Config{Follow: true})
for line := range t.Lines {
    fmt.Println(line.Text)
}
```

## Installing

    go get github.com/ActiveState/tail

## Building

To build and test the package,

    make setup
    make test

To build the command-line program `gotail`,

    cd cmd/gotail
    make
    ./gotail -h

## TODO

* Support arbitrary values for `Location`

