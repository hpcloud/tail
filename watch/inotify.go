// Copyright (c) 2013 ActiveState Software Inc. All rights reserved.

package watch

import (
	"github.com/howeyc/fsnotify"
	"os"
	"path/filepath"
	"launchpad.net/tomb"
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

func (fw *InotifyFileWatcher) BlockUntilExists(t tomb.Tomb) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()

	dirname := filepath.Dir(fw.Filename)

	// Watch for new files to be created in the parent directory.
	err = w.WatchFlags(dirname, fsnotify.FSN_CREATE)
	if err != nil {
		return err
	}
	defer w.RemoveWatch(filepath.Dir(fw.Filename))

	// Do a real check now as the file might have been created before
	// calling `WatchFlags` above.
	if _, err = os.Stat(fw.Filename); !os.IsNotExist(err) {
		// file exists, or stat returned an error.
		return err
	}

	for {
		select {
		case evt := <-w.Event:
			if evt.Name == fw.Filename {
				return nil
			}
		case <-t.Dying():
			return tomb.ErrDying
		}
	}
	panic("unreachable")
}

func (fw *InotifyFileWatcher) ChangeEvents(t tomb.Tomb, fi os.FileInfo) *FileChanges {
	changes := NewFileChanges()
	
	w, err := fsnotify.NewWatcher()
	if err != nil {
		panic(err)
	}
	err = w.Watch(fw.Filename)
	if err != nil {
		panic(err)
	}

	fw.Size = fi.Size()

	go func() {
		defer w.Close()
		defer w.RemoveWatch(fw.Filename)
		defer changes.Close()

		for {
			prevSize := fw.Size

			var evt *fsnotify.FileEvent

			select {
			case evt = <-w.Event:
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
					// XXX: no panic here
					panic(err)
				}
				fw.Size = fi.Size()

				if prevSize > 0 && prevSize > fw.Size {
					changes.NotifyTruncated()
				}else{
					changes.NotifyModified()
				}
				prevSize = fw.Size
			}
		}
	}()

	return changes
}
