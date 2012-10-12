package main

import (
	"fmt"
	"logyard/tail"
)

var samplefile = "/Users/sridharr/Library/Logs/PyPM/1.3/PyPM.log"

func main() {
	t, err := tail.TailFile(samplefile, 1000, true, true)
	if err != nil {
		panic(err)
	}
	for line := range t.Lines {
		fmt.Println(line.Text)
	}
}
