// Copyright (c) 2013 ActiveState Software Inc. All rights reserved.

package watch

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hpcloud/tail/util"

	"gopkg.in/fsnotify.v0"
	"gopkg.in/tomb.v1"
)

// InotifyFileWatcher uses inotify to monitor file changes.
type InotifyFileWatcher struct {
	Filename string
	Size     int64
}

func NewInotifyFileWatcher(filename string) *InotifyFileWatcher {
	fw := &InotifyFileWatcher{filename, 0}
	return fw
}

func (fw *InotifyFileWatcher) BlockUntilExists(t *tomb.Tomb) error {
	dirname := filepath.Dir(fw.Filename)

	// Watch for new files to be created in the parent directory.
	err := shared.WatchFlags(dirname, fsnotify.FSN_CREATE)
	if err != nil {
		return err
	}
	defer shared.RemoveWatch(dirname)

	// Do a real check now as the file might have been created before
	// calling `WatchFlags` above.
	if _, err = os.Stat(fw.Filename); !os.IsNotExist(err) {
		// file exists, or stat returned an error.
		return err
	}

	events := shared.Events(fw.Filename)

	for {
		select {
		case evt, ok := <-events:
			if !ok {
				return fmt.Errorf("inotify watcher has been closed")
			} else if evt.Name == fw.Filename {
				return nil
			}
		case <-t.Dying():
			return tomb.ErrDying
		}
	}
	panic("unreachable")
}

func (fw *InotifyFileWatcher) ChangeEvents(t *tomb.Tomb, fi os.FileInfo) *FileChanges {
	changes := NewFileChanges()

	err := shared.Watch(fw.Filename)
	if err != nil {
		go changes.NotifyDeleted()
	}

	fw.Size = fi.Size()

	go func() {
		defer shared.RemoveWatch(fw.Filename)
		defer changes.Close()

		events := shared.Events(fw.Filename)

		for {
			prevSize := fw.Size

			var evt *fsnotify.FileEvent
			var ok bool

			select {
			case evt, ok = <-events:
				if !ok {
					return
				}
			case <-t.Dying():
				return
			}

			switch {
			case evt.IsDelete():
				fallthrough

			case evt.IsRename():
				changes.NotifyDeleted()
				return

			case evt.IsModify():
				fi, err := os.Stat(fw.Filename)
				if err != nil {
					if os.IsNotExist(err) {
						changes.NotifyDeleted()
						return
					}
					// XXX: report this error back to the user
					util.Fatal("Failed to stat file %v: %v", fw.Filename, err)
				}
				fw.Size = fi.Size()

				if prevSize > 0 && prevSize > fw.Size {
					changes.NotifyTruncated()
				} else {
					changes.NotifyModified()
				}
				prevSize = fw.Size
			}
		}
	}()

	return changes
}
