# Go package for tail-ing files

A Go package striving to emulate the features of the BSD `tail` program. 

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

    make test

To build the toy command-line program `gotail`,

    cd cmd/gotail
    make
    ./gotail -h

## TODO

* Support arbitrary values for `Location`

