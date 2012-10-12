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

type Tail struct {
	Filename string
	Lines    chan *Line

	useinotify  bool
	maxlinesize int
	file        *os.File
	reader      *bufio.Reader
	watcher     FileWatcher

	stop    chan bool
	created chan bool
}

// TailFile channels the lines of a logfile along with timestamp. If
// end is true, channel only newly added lines. If retry is true, tail
// the file name (not descriptor) and retry on file open/read errors.
func TailFile(filename string, maxlinesize int, end bool, retry bool, useinotify bool) (*Tail, error) {
	t := &Tail{
		filename,
		make(chan *Line),
		useinotify,
		maxlinesize,
		nil,
		nil,
		nil,
		make(chan bool),
		make(chan bool)}

	if !useinotify {
		log.Println("Warning: not using inotify; will poll ", filename)
		t.watcher = NewPollingFileWatcher(filename)
	} else {
		t.watcher = NewInotifyFileWatcher(filename)
	}

	go t.tailFileSync(end, retry)

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

func (tail *Tail) reopen(wait bool) {
	if tail.file != nil {
		tail.file.Close()
	}
	for {
		var err error
		tail.file, err = os.Open(tail.Filename)
		if err != nil {
			if os.IsNotExist(err) && wait {
				log.Println("blocking until exists")
				err := tail.watcher.BlockUntilExists()
				if err != nil {
					panic(err)
				}
				log.Println("exists now")
				continue
			}
			log.Println(fmt.Sprintf("Unable to reopen file (%s): %s", tail.Filename, err))
		}
		return
	}
	return // unreachable
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

func (tail *Tail) tailFileSync(end bool, retry bool) {
	tail.reopen(retry)

	var changes chan bool

	// Note: seeking to end happens only at the beginning; never
	// during subsequent re-opens.
	if end {
		_, err := tail.file.Seek(0, 2) // seek to end of the file
		if err != nil {
			// TODO: don't panic here
			panic(fmt.Sprintf("seek error: %s", err))
		}
	}

	tail.reader = bufio.NewReaderSize(tail.file, tail.maxlinesize)

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

				//log.Println("WAITING ", tail.Filename)
				_, ok := <-changes
				//log.Println("RECEIVED ", tail.Filename)

				if !ok {
					// file got deleted/renamed
					if retry {
						log.Printf("File %s has been moved (logrotation?); reopening..", tail.Filename)
						tail.reopen(retry)
						log.Printf("File %s has been reopened.", tail.Filename)
						tail.reader = bufio.NewReaderSize(tail.file, tail.maxlinesize)
						changes = nil
						continue
					} else {
						log.Printf("File %s has gone away; skipping this file.\n", tail.Filename)
						tail.close()
						return
					}
				}
			}
		}

		// stop the tailer if requested.
		// FIXME: won't happen promptly; http://bugs.activestate.com/show_bug.cgi?id=95718#c3
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
