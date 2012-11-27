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

type Config struct {
	Location    int  // -n
	Follow      bool // -f
	ReOpen      bool // -F
	MustExist   bool // if false, wait for the file to exist before beginning to tail.
	Poll        bool // if true, do not use inotify but use polling
	MaxLineSize int  // if > 0, limit the line size (rest of the line would be returned as next lines)
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
							tail.reader = bufio.NewReader(tail.file)
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
