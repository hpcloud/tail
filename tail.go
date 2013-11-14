// Copyright (c) 2013 ActiveState Software Inc. All rights reserved.

package tail

import (
	"bufio"
	"fmt"
	"github.com/ActiveState/tail/util"
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
	Err  error // Error from tail
}

// NewLine returns a Line with present time.
func NewLine(text string) *Line {
	return &Line{text, time.Now(), nil}
}

// SeekInfo represents arguments to `os.Seek`
type SeekInfo struct {
	Offset int64
	Whence int // os.SEEK_*
}

// Config is used to specify how a file must be tailed.
type Config struct {
	// File-specifc
	Location  *SeekInfo // Seek to this location before tailing
	ReOpen    bool      // Reopen recreated files (tail -F)
	MustExist bool      // Fail early if the file does not exist
	Poll      bool      // Poll for file changes instead of using inotify
	LimitRate int64     // Maximum read rate (lines per second)

	// Generic IO
	Follow      bool // Continue looking for new lines (tail -f)
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
	rateMon *RateMonitor

	tomb.Tomb // provides: Done, Kill, Dying
}

// TailFile begins tailing the file. Output stream is made available
// via the `Tail.Lines` channel. To handle errors during tailing,
// invoke the `Wait` or `Err` method after finishing reading from the
// `Lines` channel.
func TailFile(filename string, config Config) (*Tail, error) {
	if config.ReOpen && !config.Follow {
		util.Fatal("cannot set ReOpen without Follow.")
	}

	t := &Tail{
		Filename: filename,
		Lines:    make(chan *Line),
		Config:   config}

	t.rateMon = new(RateMonitor)

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

// Return the file's current position, like stdio's ftell().
// But this value is not very accurate.
// it may readed one line in the chan(tail.Lines),
// so it may lost one line.
func (tail *Tail) Tell() (offset int64, err error) {
	if tail.file == nil {
		return
	}
	offset, err = tail.file.Seek(0, os.SEEK_CUR)
	if err == nil {
		offset -= int64(tail.reader.Buffered())
	}
	return
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
				if err := tail.watcher.BlockUntilExists(&tail.Tomb); err != nil {
					if err == tomb.ErrDying {
						return err
					}
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
			if err != tomb.ErrDying {
				tail.Kill(err)
			}
			return
		}
	}

	// Seek to requested location on first open of the file.
	if tail.Location != nil {
		_, err := tail.file.Seek(tail.Location.Offset, tail.Location.Whence)
		// log.Printf("Seeked %s - %+v\n", tail.Filename, tail.Location)
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
				cooloff := !tail.sendLine(line)
				if cooloff {
					msg := "Too much activity; entering a cool-off period"
					tail.Lines <- &Line{
						msg,
						time.Now(),
						fmt.Errorf(msg)}
					// Wait a second before seeking till the end of
					// file when rate limit is reached.
					select {
					case <-time.After(time.Second):
					case <-tail.Dying():
						return
					}
					_, err := tail.file.Seek(0, 2) // Seek to fine end
					if err != nil {
						tail.Killf("Seek error on %s: %s", tail.Filename, err)
						return
					}
				}
			}
		case io.EOF:
			if !tail.Follow {
				return
			}
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
		tail.changes = tail.watcher.ChangeEvents(&tail.Tomb, st)
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
// if necessary. Return false if rate limit is reached.
func (tail *Tail) sendLine(line []byte) bool {
	now := time.Now()
	nowUnix := now.Unix()
	lines := []string{string(line)}

	// Split longer lines
	if tail.MaxLineSize > 0 && len(line) > tail.MaxLineSize {
		lines = util.PartitionString(
			string(line), tail.MaxLineSize)
	}

	for _, line := range lines {
		tail.Lines <- &Line{line, now, nil}
		rate := tail.rateMon.Tick(nowUnix)
		if tail.LimitRate > 0 && rate > tail.LimitRate {
			log.Printf("Rate limit (%v < %v) reached on file (%v); entering 1s cooloff period.\n",
				tail.LimitRate,
				rate,
				tail.Filename)
			return false
		}
	}

	return true
}

// Cleanup removes inotify watches added by the tail package. This function is
// meant to be invoked from a process's exit handler. Linux kernel will not
// automatically remove inotify watches after the process exits.
func Cleanup() {
	watch.Cleanup()
}
