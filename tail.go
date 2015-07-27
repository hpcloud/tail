// Copyright (c) 2013 ActiveState Software Inc. All rights reserved.

package tail

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"time"

	"github.com/masahide/tail/ratelimiter"
	"github.com/masahide/tail/util"
	"github.com/masahide/tail/watch"
	"gopkg.in/tomb.v1"
)

var (
	ErrStop = fmt.Errorf("tail should now stop")
)

const (
	NewLineNotify int = iota
	NewFileNotify
	TickerNotify
)

type Line struct {
	Time       time.Time
	Text       []byte
	Filename   string
	Offset     int64
	OpenTime   time.Time
	Err        error // Error from tail
	NotifyType int
}

// NewLine returns a Line with present time.
func NewLine(text []byte) *Line {
	return &Line{Text: text, Time: time.Now(), Err: nil}
}

// SeekInfo represents arguments to `os.Seek`
type SeekInfo struct {
	Offset int64
	Whence int // os.SEEK_*
}

// Config is used to specify how a file must be tailed.
type Config struct {
	// File-specifc
	Location    *SeekInfo     // Seek to this location before tailing
	ReOpen      bool          // Reopen recreated files (tail -F)
	ReOpenDelay time.Duration // Reopen Delay
	MustExist   bool          // Fail early if the file does not exist
	Poll        bool          // Poll for file changes instead of using inotify
	RateLimiter *ratelimiter.LeakyBucket

	// Generic IO
	Follow         bool          // Continue looking for new lines (tail -f)
	MaxLineSize    int           // If non-zero, split longer lines into multiple lines
	NotifyInterval time.Duration // Notice interval of the elapsed time

	// Logger, when nil, is set to tail.DefaultLogger
	// To disable logging: set field to tail.DiscardingLogger
	Logger *log.Logger
}

type Tail struct {
	Filename string
	Lines    chan *Line
	Config

	file     *os.File
	reader   *bufio.Reader
	tracker  *watch.InotifyTracker
	ticker   *time.Ticker
	openTime time.Time

	watcher      watch.FileWatcher
	changes      *watch.FileChanges
	reOpenNotify <-chan time.Time

	tomb.Tomb // provides: Done, Kill, Dying
}

var (
	// DefaultLogger is used when Config.Logger == nil
	DefaultLogger = log.New(os.Stderr, "", log.LstdFlags)
	// DiscardingLogger can be used to disable logging output
	DiscardingLogger = log.New(ioutil.Discard, "", 0)
)

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
		Config:   config,
	}

	// when Logger was not specified in config, use default logger
	if t.Logger == nil {
		t.Logger = log.New(os.Stderr, "", log.LstdFlags)
	}

	if t.Poll {
		t.watcher = watch.NewPollingFileWatcher(filename)
	} else {
		t.tracker = watch.NewInotifyTracker()
		w, err := t.tracker.NewWatcher()
		if err != nil {
			return nil, err
		}
		t.watcher = watch.NewInotifyFileWatcher(filename, w)
	}

	if t.MustExist {
		var err error
		t.file, err = OpenFile(t.Filename)
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
		tail.file, err = OpenFile(tail.Filename)
		if err != nil {
			if os.IsNotExist(err) {
				tail.Logger.Printf("Waiting for %s to appear...", tail.Filename)
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
	line, err := tail.reader.ReadBytes(byte('\n'))
	if err != nil {
		// Note ReadString "returns the data read before the error" in
		// case of an error, including EOF, so we return it as is. The
		// caller is expected to process it if err is EOF.
		return line, err
	}

	//line = bytes.TrimRight(line, "\n")

	return line, err
}

func (tail *Tail) tailFileSync() {
	defer tail.Done()
	defer tail.close()

	tail.reOpenNotify = make(chan time.Time)
	tail.ticker = &time.Ticker{}
	if tail.NotifyInterval != 0 {
		tail.ticker = time.NewTicker(tail.NotifyInterval)
	}
	defer tail.ticker.Stop()

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
	offset := int64(0)
	if tail.Location != nil {
		offset = tail.Location.Offset
		_, err := tail.file.Seek(offset, tail.Location.Whence)
		tail.Logger.Printf("Seeked %s - %+v\n", tail.Filename, tail.Location)
		if err != nil {
			tail.Killf("Seek error on %s: %s", tail.Filename, err)
			return
		}
	}

	tail.openReader()
	tail.Lines <- &Line{NotifyType: NewFileNotify, Filename: tail.Filename, Offset: offset, Time: time.Now(), OpenTime: tail.openTime}

	// Read line by line.
	for {
		// grab the position in case we need to back up in the event of a half-line
		offset, err := tail.Tell()
		if err != nil {
			tail.Kill(err)
			return
		}

		line, err := tail.readLine()

		// Process `line` even if err is EOF.
		if err == nil {
			cooloff := !tail.sendLine(line)
			if cooloff {
				// Wait a second before seeking till the end of
				// file when rate limit is reached.
				msg := fmt.Sprintf(
					"Too much log activity; waiting a second " +
						"before resuming tailing")
				tail.Lines <- &Line{Text: []byte(msg), Time: time.Now(), Filename: tail.Filename, OpenTime: tail.openTime, Offset: offset, Err: fmt.Errorf(msg)}
				select {
				case <-time.After(time.Second):
				case <-tail.Dying():
					return
				}
				err = tail.seekEnd()
				if err != nil {
					tail.Kill(err)
					return
				}
			}
		} else if err == io.EOF {
			if !tail.Follow {
				if len(line) != 0 {
					tail.sendLine(line)
				}
				return
			}

			if tail.Follow && len(line) != 0 {
				// this has the potential to never return the last line if
				// it's not followed by a newline; seems a fair trade here
				err := tail.seekTo(SeekInfo{Offset: offset, Whence: 0})
				if err != nil {
					tail.Kill(err)
					return
				}
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
		} else {
			// non-EOF error
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

	for {

		select {
		case <-tail.ticker.C:
			offset, err := tail.Tell()
			if err != nil {
				return err
			}
			tail.Lines <- &Line{NotifyType: TickerNotify, Time: time.Now(), Filename: tail.Filename, OpenTime: tail.openTime, Offset: offset}
			continue
		case <-tail.changes.Modified:
			return nil
		case <-tail.changes.Deleted:
			if tail.ReOpen {
				tail.Logger.Printf("moved/deleted file %s ... Reopen delay %s", tail.Filename, tail.ReOpenDelay)
				tail.reOpenNotify = time.After(tail.ReOpenDelay)
				continue
			} else {
				tail.changes = nil
				tail.Logger.Printf("Stopping tail as file no longer exists: %s", tail.Filename)
				return ErrStop
			}
		case <-tail.changes.Truncated:
			// Always reopen truncated files (Follow is true)
			tail.Logger.Printf("Re-opening truncated file %s ...", tail.Filename)
			if err := tail.reopen(); err != nil {
				return err
			}
			tail.Logger.Printf("Successfully reopened truncated %s", tail.Filename)
			tail.openReader()
			return nil
		case <-tail.reOpenNotify:
			tail.changes = nil
			// XXX: we must not log from a library.
			tail.Logger.Printf("Re-opening moved/deleted file %s ...", tail.Filename)
			if err := tail.reopen(); err != nil {
				return err
			}
			tail.Logger.Printf("Successfully reopened %s", tail.Filename)
			tail.openReader()
			return nil
		case <-tail.Dying():
			return ErrStop
		}
		panic("unreachable")
	}
}

func (tail *Tail) openReader() {
	if tail.MaxLineSize > 0 {
		// add 2 to account for newline characters
		tail.reader = bufio.NewReaderSize(tail.file, tail.MaxLineSize+2)
	} else {
		tail.reader = bufio.NewReader(tail.file)
	}
	fi, err := os.Stat(tail.Filename)
	if err != nil {
		tail.openTime = time.Now()
		return
	}
	tail.openTime = fi.ModTime()
}

func (tail *Tail) seekEnd() error {
	return tail.seekTo(SeekInfo{Offset: 0, Whence: 2})
}

func (tail *Tail) seekTo(pos SeekInfo) error {
	_, err := tail.file.Seek(pos.Offset, pos.Whence)
	if err != nil {
		return fmt.Errorf("Seek error on %s: %s", tail.Filename, err)
	}
	// Reset the read buffer whenever the file is re-seek'ed
	tail.reader.Reset(tail.file)
	return nil
}

// sendLine sends the line(s) to Lines channel, splitting longer lines
// if necessary. Return false if rate limit is reached.
func (tail *Tail) sendLine(line []byte) bool {
	now := time.Now()
	offset, err := tail.Tell()
	if err != nil {
		tail.Kill(err)
		return true
	}
	tail.Lines <- &Line{NotifyType: NewLineNotify, Text: line, Time: now, Filename: tail.Filename, OpenTime: tail.openTime, Offset: offset}

	if tail.Config.RateLimiter != nil {
		ok := tail.Config.RateLimiter.Pour(uint16(1))
		if !ok {
			tail.Logger.Printf("Leaky bucket full (%v); entering 1s cooloff period.\n",
				tail.Filename)
			return false
		}
	}

	return true
}

// Cleanup removes inotify watches added by the tail package. This function is
// meant to be invoked from a process's exit handler. Linux kernel may not
// automatically remove inotify watches after the process exits.
func (tail *Tail) Cleanup() {
	if tail.tracker != nil {
		tail.tracker.CloseAll()
	}
}
