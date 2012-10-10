package tail

import (
	"bufio"
	"fmt"
	"github.com/howeyc/fsnotify"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"
)

type Line struct {
	Text     string
	UnixTime int64
}

type Tail struct {
	Filename string
	Lines    chan *Line

	maxlinesize int
	file        *os.File
	reader      *bufio.Reader
	watcher     *fsnotify.Watcher

	stop    chan bool
	created chan bool
}

// TailFile channels the lines of a logfile along with timestamp. If
// end is true, channel only newly added lines. If retry is true, tail
// the file name (not descriptor) and retry on file open/read errors.
func TailFile(filename string, maxlinesize int, end bool, retry bool) (*Tail, error) {
	watcher, err := fileCreateWatcher(filename)
	if err != nil {
		return nil, err
	}
	t := &Tail{
		filename,
		make(chan *Line),
		maxlinesize,
		nil,
		nil,
		watcher,
		make(chan bool),
		make(chan bool)}

	go t.tailFileSync(end, retry)

	return t, nil
}

func (tail *Tail) Stop() {
	tail.stop <- true
	tail.close()
}

func (tail *Tail) close() {
	close(tail.Lines)
	tail.watcher.Close()
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
				for {
					evt := <-tail.watcher.Event
					if evt.Name == tail.Filename {
						break
					}
				}
				continue
			}
			// TODO: don't panic here
			panic(fmt.Sprintf("can't open file: %s", err))
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

	every2Seconds := time.Tick(2 * time.Second)

	for {
		line, err := tail.readLine()

		if err != nil && err != io.EOF {
			log.Println("Error reading file; skipping this file - ", err)
			tail.close()
			return
		}

		// sleep for 0.1s on inactive files, else we cause too much I/O activity
		if err == io.EOF {
			time.Sleep(100 * time.Millisecond)
		}

		if line != nil {
			tail.Lines <- &Line{string(line), getCurrentTime()}
		}

		select {
		case <-every2Seconds: // periodically stat the file to check for possibly deletion.
			if _, err := tail.file.Stat(); os.IsNotExist(err) {
				if retry {
					log.Printf("File %s has gone away; attempting to reopen it.\n", tail.Filename)
					tail.reopen(retry)
					tail.reader = bufio.NewReaderSize(tail.file, tail.maxlinesize)
					continue
				} else {
					log.Printf("File %s has gone away; skipping this file.\n", tail.Filename)
					tail.close()
					return
				}
			}
		case evt := <-tail.watcher.Event:
			if evt.Name == tail.Filename {
				log.Printf("File %s has been moved (logrotation?); reopening..", tail.Filename)
				tail.reopen(retry)
				tail.reader = bufio.NewReaderSize(tail.file, tail.maxlinesize)
				continue
			}
		case <-tail.stop: // stop the tailer if requested
			return
		default:
		}
	}
}

// returns the watcher for file create events
func fileCreateWatcher(filename string) (*fsnotify.Watcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	// watch on parent directory because the file may not exit.
	err = watcher.WatchFlags(filepath.Dir(filename), fsnotify.FSN_CREATE)
	if err != nil {
		return nil, err
	}

	return watcher, nil
}

// get current time in unix timestamp
func getCurrentTime() int64 {
	return time.Now().UTC().Unix()
}
