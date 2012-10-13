package main

import (
	"fmt"
	"logyard/tail"
	"flag"
)

var samplefile = "/tmp/test"

func args2config() tail.Config {
	config := tail.Config{Follow: true}
	flag.IntVar(&config.Location, "n", 0, "tail from the last Nth location")
	flag.BoolVar(&config.Follow, "f", false, "wait for additional data to be appended to the file")
	flag.BoolVar(&config.ReOpen, "F", false, "follow, and track file rename/rotation")
	flag.Parse()
	if config.ReOpen {
		config.Follow = true
	}
	return config
}

func main() {
	t, err := tail.TailFile(samplefile, args2config())
	if err != nil {
		fmt.Println(err)
		return
	}
	for line := range t.Lines {
		fmt.Println(line.Text)
	}
	err = t.Wait()
	if err != nil {
		fmt.Println(err)
	}
}
