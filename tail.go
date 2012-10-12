package tail

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"time"
)

type Line struct {
	Text     string
	UnixTime int64
}

type Config struct {
	Location    int  // -n
	Follow      bool // -f
	ReOpen      bool // -F
	MustExist   bool // if false, wait for the file to exist before beginning to tail.
	Poll        bool // if true, do not use inotify but use polling
	MaxLineSize int  // if > 0, limit the line size (discarding the rest)
}

type Tail struct {
	Filename string
	Lines    chan *Line
	Config

	file    *os.File
	reader  *bufio.Reader
	watcher FileWatcher

	stop    chan bool
	created chan bool
}

// TailFile channels the lines of a logfile along with timestamp. If
// end is true, channel only newly added lines. If retry is true, tail
// the file name (not descriptor) and retry on file open/read errors.
// func TailFile(filename string, maxlinesize int, end bool, retry bool, useinotify bool) (*Tail, error) {
func TailFile(filename string, config Config) (*Tail, error) {
	if !(config.Location == 0 || config.Location == -1) {
		panic("only 0/-1 values are supported for Location")
	}

	t := &Tail{
		filename,
		make(chan *Line),
		config,
		nil,
		nil,
		nil,
		make(chan bool),
		make(chan bool)}

	if t.Poll {
		log.Println("Warning: not using inotify; will poll ", filename)
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

func (tail *Tail) Stop() {
	tail.stop <- true
	tail.close()
}

func (tail *Tail) close() {
	close(tail.Lines)
	if tail.file != nil {
		tail.file.Close()
	}
}

func (tail *Tail) reopen() {
	if tail.file != nil {
		tail.file.Close()
	}
	for {
		var err error
		tail.file, err = os.Open(tail.Filename)
		if err != nil {
			if os.IsNotExist(err) {
				err := tail.watcher.BlockUntilExists()
				if err != nil {
					// TODO: use error channels
					log.Fatalf("cannot watch for file creation -- %s", tail.Filename, err)
				}
				continue
			}
		}
		break
	}
}

func (tail *Tail) readLine() ([]byte, error) {
	line, isPrefix, err := tail.reader.ReadLine()

	if isPrefix && err == nil {
		// line is longer than what we can accept. 
		// ignore the rest of this line.
		for {
			_, isPrefix, err := tail.reader.ReadLine()
			if !isPrefix || err != nil {
				return line, err
			}
		}
	}
	return line, err
}

func (tail *Tail) tailFileSync() {
	if !tail.MustExist {
		tail.reopen()
	}

	var changes chan bool

	// Note: seeking to end happens only at the beginning; never
	// during subsequent re-opens.
	if tail.Location == 0 {
		_, err := tail.file.Seek(0, 2) // seek to end of the file
		if err != nil {
			// TODO: don't panic here
			panic(fmt.Sprintf("seek error: %s", err))
		}
	}

	tail.reader = bufio.NewReaderSize(tail.file, tail.MaxLineSize)

	for {
		line, err := tail.readLine()

		if err == nil {
			if line != nil {
				tail.Lines <- &Line{string(line), getCurrentTime()}
			}
		} else {
			if err != io.EOF {
				log.Println("Error reading file; skipping this file - ", err)
				tail.close()
				return
			}

			// When end of file is reached, wait for more data to
			// become available. Wait strategy is based on the
			// `tail.watcher` implementation (inotify or polling).
			if err == io.EOF {
				if changes == nil {
					changes = tail.watcher.ChangeEvents()
				}

				select {
				case _, ok := <-changes:
					if !ok {
						// file got deleted/renamed
						if tail.ReOpen {
							log.Printf("File %s has been moved (logrotation?); reopening..", tail.Filename)
							tail.reopen()
							log.Printf("File %s has been reopened.", tail.Filename)
							tail.reader = bufio.NewReaderSize(tail.file, tail.MaxLineSize)
							changes = nil
							continue
						} else {
							log.Printf("File %s has gone away; skipping this file.\n", tail.Filename)
							tail.close()
							return
						}
					}
				case <-tail.stop:
					// stop the tailer if requested.
					// FIXME: respect DRY (see below)
					return
				}

			}
		}

		// stop the tailer if requested.
		select {
		case <-tail.stop:
			return
		default:
		}
	}
}

// get current time in unix timestamp
func getCurrentTime() int64 {
	return time.Now().UTC().Unix()
}
