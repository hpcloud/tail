// Copyright (c) 2013 ActiveState Software Inc. All rights reserved.

package tail

import (
	"bufio"
	"fmt"
	"io"
	"launchpad.net/tomb"
	"log"
	"os"
	"time"
)

type Line struct {
	Text     string
	Time     time.Time
}

// Tail configuration
type Config struct {
	Location    int  // Tail from last N lines (tail -n)
	Follow      bool // Continue looking for new lines (tail -f)
	ReOpen      bool // Reopen recreated files (tail -F)
	MustExist   bool // Fail early if the file does not exist
	Poll        bool // Poll for file changes instead of using inotify
	MaxLineSize int  // If non-zero, split longer lines into multiple lines
}

type Tail struct {
	Filename string
	Lines    chan *Line
	Config

	file    *os.File
	reader  *bufio.Reader
	watcher FileWatcher

	tomb.Tomb // provides: Done, Kill, Dying
}

// TailFile begins tailing the file with the specified
// configuration. Output stream is made available via the `Tail.Lines`
// channel. To handle errors during tailing, invoke the `Wait` method
// after finishing reading from the `Lines` channel.
func TailFile(filename string, config Config) (*Tail, error) {
	if !(config.Location == 0 || config.Location == -1) {
		panic("only 0/-1 values are supported for Location.")
	}

	if config.ReOpen && !config.Follow {
		panic("cannot set ReOpen without Follow.")
	}

	if !config.Follow {
		panic("Follow=false is not supported.")
	}

	t := &Tail{
		Filename: filename,
		Lines:    make(chan *Line),
		Config:   config}

	if t.Poll {
		t.watcher = NewPollingFileWatcher(filename)
	} else {
		t.watcher = NewInotifyFileWatcher(filename)
	}

	if t.MustExist {
		var err error
		t.file, err = os.Open(t.Filename)
		if err != nil {
			return nil, err
		}
	}

	go t.tailFileSync()

	return t, nil
}

func (tail *Tail) Stop() error {
	tail.Kill(nil)
	return tail.Wait()
}

func (tail *Tail) close() {
	close(tail.Lines)
	if tail.file != nil {
		tail.file.Close()
	}
}

func (tail *Tail) reopen() error {
	if tail.file != nil {
		tail.file.Close()
	}
	for {
		var err error
		tail.file, err = os.Open(tail.Filename)
		if err != nil {
			if os.IsNotExist(err) {
				log.Printf("Waiting for %s to appear...", tail.Filename)
				if err := tail.watcher.BlockUntilExists(); err != nil {
					return fmt.Errorf("Failed to detect creation of %s: %s", tail.Filename, err)
				}
				continue
			}
			return fmt.Errorf("Unable to open file %s: %s", tail.Filename, err)
		}
		break
	}
	return nil
}

func (tail *Tail) readLine() ([]byte, error) {
	line, _, err := tail.reader.ReadLine()
	return line, err
}

func (tail *Tail) tailFileSync() {
	defer tail.Done()

	if !tail.MustExist {
		err := tail.reopen()
		if err != nil {
			tail.close()
			tail.Kill(err)
			return
		}
	}

	var changes chan bool

	// Note: seeking to end happens only at the beginning of tail;
	// never during subsequent re-opens.
	if tail.Location == 0 {
		_, err := tail.file.Seek(0, 2) // seek to end of the file
		if err != nil {
			tail.close()
			tail.Killf("Seek error on %s: %s", tail.Filename, err)
			return
		}
	}

	tail.reader = bufio.NewReader(tail.file)

	for {
		line, err := tail.readLine()

		if err == nil {
			if line != nil {
				now := time.Now()
				if tail.MaxLineSize > 0 && len(line) > tail.MaxLineSize {
					for _, line := range partitionString(string(line), tail.MaxLineSize) {
						tail.Lines <- &Line{line, now}
					}
				}else{
					tail.Lines <- &Line{string(line), now}
				}
			}
		} else {
			if err != io.EOF {
				tail.close()
				tail.Killf("Error reading %s: %s", tail.Filename, err)
				return
			}

			// When end of file is reached, wait for more data to
			// become available. Wait strategy is based on the
			// `tail.watcher` implementation (inotify or polling).
			if err == io.EOF {
				if changes == nil {
					st, err := tail.file.Stat()
					if err != nil {
						tail.close()
						tail.Kill(err)
						return
					}
					changes = tail.watcher.ChangeEvents(st)
				}

				select {
				case _, ok := <-changes:
					if !ok {
						changes = nil // XXX: how to kill changes' goroutine?

						log.Println("Changes channel is closed.")
						// File got deleted/renamed/truncated.
						if tail.ReOpen {
							// TODO: no logging in a library?
							log.Printf("Re-opening moved/deleted/truncated file %s ...", tail.Filename)
							err := tail.reopen()
							if err != nil {
								tail.close()
								tail.Kill(err)
								return
							}
							log.Printf("Successfully reopened %s", tail.Filename)
							tail.reader = bufio.NewReader(tail.file)
							
							continue
						} else {
							log.Printf("Finishing because file has been moved/deleted: %s", tail.Filename)
							tail.close()
							return
						}
					}
				case <-tail.Dying():
					log.Println("Dying..")
					tail.close()
					return
				}
			}
		}

		select {
		case <-tail.Dying():
			tail.close()
			return
		default:
		}
	}
}

// partitionString partitions the string into chunks of given size,
// with the last chunk of variable size.
func partitionString(s string, chunkSize int) []string {
	if chunkSize <= 0 {
		panic("invalid chunkSize")
	}
	length := len(s)
	chunks := 1 + length/chunkSize 
	start := 0
	end := chunkSize
	parts := make([]string, 0, chunks)
	for {
		if end > length {
			end = length
		}
		parts = append(parts, s[start:end])
		if end == length {
			break
		}
		start, end = end, end+chunkSize
	}
	return parts
}
