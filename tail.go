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

	tomb.Tomb // provides: Done, Kill, Dying
}

// TailFile channels the lines of a logfile along with timestamp. If
// end is true, channel only newly added lines. If retry is true, tail
// the file name (not descriptor) and retry on file open/read errors.
// func TailFile(filename string, maxlinesize int, end bool, retry bool, useinotify bool) (*Tail, error) {
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
				err := tail.watcher.BlockUntilExists()
				if err != nil {
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

	tail.reader = bufio.NewReaderSize(tail.file, tail.MaxLineSize)

	for {
		line, err := tail.readLine()

		if err == nil {
			if line != nil {
				tail.Lines <- &Line{string(line), getCurrentTime()}
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
					changes = tail.watcher.ChangeEvents()
				}

				select {
				case _, ok := <-changes:
					if !ok {
						// File got deleted/renamed
						if tail.ReOpen {
							// TODO: no logging in a library?
							log.Printf("Re-opening moved/deleted file %s ...", tail.Filename)
							err := tail.reopen()
							if err != nil {
								tail.close()
								tail.Kill(err)
								return
							}
							log.Printf("Successfully reopened %s", tail.Filename)
							tail.reader = bufio.NewReaderSize(tail.file, tail.MaxLineSize)
							changes = nil // XXX: how to kill changes' goroutine?
							continue
						} else {
							log.Printf("Finishing because file has been moved/deleted: %s", tail.Filename)
							tail.close()
							return
						}
					}
				case <-tail.Dying():
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

// getCurrentTime returns the current time as UNIX timestamp
func getCurrentTime() int64 {
	return time.Now().UTC().Unix()
}
