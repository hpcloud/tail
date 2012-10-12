# Tail implementation in Go

A Go package striving to emulate the BSD `tail` program. 

```Go
t := tail.TailFile("/var/log/nginx.log", 1000, true, true)
for line := range t.Lines {
    fmt.Println(line.Text)
}
```

## TODO

* tests
* command line program (`tail -f ...`)
* refactor: use Config? `NewTail(tail.Config{Filename: "", Follow: tail.FOLLOW_NAME})`
* refactor: get rid of 'end' flag; allow `-n <number>` with `-n -1`
  for end.
