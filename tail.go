// Copyright (c) 2013 ActiveState Software Inc. All rights reserved.

package tail

import (
	"bufio"
	"fmt"
	"github.com/ActiveState/tail/watch"
	"io"
	"launchpad.net/tomb"
	"log"
	"os"
	"time"
)

var (
	ErrStop = fmt.Errorf("tail should now stop")
)

type Line struct {
	Text string
	Time time.Time
}

// Config is used to specify how a file must be tailed.
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
	watcher watch.FileWatcher
	changes *watch.FileChanges

	tomb.Tomb // provides: Done, Kill, Dying
}

// TailFile begins tailing the file. Output stream is made available
// via the `Tail.Lines` channel. To handle errors during tailing,
// invoke the `Wait` or `Err` method after finishing reading from the
// `Lines` channel.
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
		t.watcher = watch.NewPollingFileWatcher(filename)
	} else {
		t.watcher = watch.NewInotifyFileWatcher(filename)
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

// Stop stops the tailing activity.
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
				if err := tail.watcher.BlockUntilExists(tail.Tomb); err != nil {
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
	defer tail.close()

	if !tail.MustExist {
		// deferred first open.
		err := tail.reopen()
		if err != nil {
			tail.Kill(err)
			return
		}
	}

	// Seek to requested location on first open of the file.
	if tail.Location == 0 {
		_, err := tail.file.Seek(0, 2) // Seek to the file end
		if err != nil {
			tail.Killf("Seek error on %s: %s", tail.Filename, err)
			return
		}
	}

	tail.reader = bufio.NewReader(tail.file)

	// Read line by line.
	for {
		line, err := tail.readLine()

		switch err {
		case nil:
			if line != nil {
				tail.sendLine(line)
			}
		case io.EOF:
			// When EOF is reached, wait for more data to become
			// available. Wait strategy is based on the `tail.watcher`
			// implementation (inotify or polling).
			err := tail.waitForChanges()
			if err != nil {
				if err != ErrStop {
					tail.Kill(err)
				}
				return
			}
		default: // non-EOF error
			tail.Killf("Error reading %s: %s", tail.Filename, err)
			return
		}

		select {
		case <-tail.Dying():
			return
		default:
		}
	}
}

// waitForChanges waits until the file has been appended, deleted,
// moved or truncated. When moved or deleted - the file will be
// reopened if ReOpen is true. Truncated files are always reopened.
func (tail *Tail) waitForChanges() error {
	if tail.changes == nil {
		st, err := tail.file.Stat()
		if err != nil {
			return err
		}
		tail.changes = tail.watcher.ChangeEvents(tail.Tomb, st)
	}

	select {
	case <-tail.changes.Modified:
		return nil
	case <-tail.changes.Deleted:
		tail.changes = nil
		if tail.ReOpen {
			// XXX: we must not log from a library.
			log.Printf("Re-opening moved/deleted file %s ...", tail.Filename)
			if err := tail.reopen(); err != nil {
				return err
			}
			log.Printf("Successfully reopened %s", tail.Filename)
			tail.reader = bufio.NewReader(tail.file)
			return nil
		} else {
			log.Printf("Stopping tail as file no longer exists: %s", tail.Filename)
			return ErrStop
		}
	case <-tail.changes.Truncated:
		// Always reopen truncated files (Follow is true)
		log.Printf("Re-opening truncated file %s ...", tail.Filename)
		if err := tail.reopen(); err != nil {
			return err
		}
		log.Printf("Successfully reopened truncated %s", tail.Filename)
		tail.reader = bufio.NewReader(tail.file)
		return nil
	case <-tail.Dying():
		return ErrStop
	}
	panic("unreachable")
}

// sendLine sends the line(s) to Lines channel, splitting longer lines
// if necessary.
func (tail *Tail) sendLine(line []byte) {
	now := time.Now()
	lines := []string{string(line)}

	// Split longer lins
	if tail.MaxLineSize > 0 && len(line) > tail.MaxLineSize {
		lines = partitionString(
			string(line), tail.MaxLineSize)
	}

	for _, line := range lines {
		tail.Lines <- &Line{line, now}
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
