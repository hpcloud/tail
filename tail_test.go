// TODO:
//  * repeat all the tests with Poll:true

package tail

import (
	_ "fmt"
	"io/ioutil"
	"os"
	"testing"
	"time"
)

func init() {
	err := os.RemoveAll(".test")
	if err != nil {
		panic(err)
	}
}

func TestMustExist(t *testing.T) {
	tail, err := TailFile("/no/such/file", Config{Follow: true, MustExist: true})
	if err == nil {
		t.Error("MustExist:true is violated")
		tail.Stop()
	}
	tail, err = TailFile("/no/such/file", Config{Follow: true, MustExist: false})
	if err != nil {
		t.Error("MustExist:false is violated")
	}
	tail.Stop()
	_, err = TailFile("README.md", Config{Follow: true, MustExist: true})
	if err != nil {
		t.Error("MustExist:true on an existing file is violated")
	}
	tail.Stop()
}

func TestMaxLineSize(t *testing.T) {
	fix := NewFixture("maxlinesize", t)
	fix.CreateFile("test.txt", "hello\nworld\n")
	tail := fix.StartTail("test.txt", Config{Follow: true, Location: -1, MaxLineSize: 3})
	go fix.VerifyTail(tail, []string{"hel", "lo", "wor", "ld"})

	// Delete after a reasonable delay, to give tail sufficient time
	// to read all lines.
	<-time.After(100 * time.Millisecond)
	fix.RemoveFile("test.txt")
	tail.Stop()
}

func TestLocationFull(t *testing.T) {
	fix := NewFixture("location-full", t)
	fix.CreateFile("test.txt", "hello\nworld\n")
	tail := fix.StartTail("test.txt", Config{Follow: true, Location: -1})
	go fix.VerifyTail(tail, []string{"hello", "world"})

	// Delete after a reasonable delay, to give tail sufficient time
	// to read all lines.
	<-time.After(100 * time.Millisecond)
	fix.RemoveFile("test.txt")
	tail.Stop()
}

func TestLocationEnd(t *testing.T) {
	fix := NewFixture("location-end", t)
	fix.CreateFile("test.txt", "hello\nworld\n")
	tail := fix.StartTail("test.txt", Config{Follow: true, Location: 0})
	go fix.VerifyTail(tail, []string{"more", "data"})

	<-time.After(100 * time.Millisecond)
	fix.AppendFile("test.txt", "more\ndata\n")

	// Delete after a reasonable delay, to give tail sufficient time
	// to read all lines.
	<-time.After(100 * time.Millisecond)
	fix.RemoveFile("test.txt")
	tail.Stop()
}

func TestReOpen(t *testing.T) {
	fix := NewFixture("reopen", t)
	fix.CreateFile("test.txt", "hello\nworld\n")
	tail := fix.StartTail("test.txt", Config{Follow: true, ReOpen: true, Location: -1})
	go fix.VerifyTail(tail, []string{"hello", "world", "more", "data", "endofworld"})

	// deletion must trigger reopen
	<-time.After(100 * time.Millisecond)
	fix.RemoveFile("test.txt")
	<-time.After(100 * time.Millisecond)
	fix.CreateFile("test.txt", "more\ndata\n")

	// rename must trigger reopen
	<-time.After(100 * time.Millisecond)
	fix.RenameFile("test.txt", "test.txt.rotated")
	<-time.After(100 * time.Millisecond)
	fix.CreateFile("test.txt", "endofworld")

	// Delete after a reasonable delay, to give tail sufficient time
	// to read all lines.
	<-time.After(100 * time.Millisecond)
	fix.RemoveFile("test.txt")
	
	tail.Stop()
}


// Test library

type Fixture struct {
	Name string
	path string
	t    *testing.T
}

func NewFixture(name string, t *testing.T) Fixture {
	fix := Fixture{name, ".test/" + name, t}
	err := os.MkdirAll(fix.path, os.ModeTemporary|0700)
	if err != nil {
		t.Fatal(err)
	}
	return fix
}

func (fix Fixture) CreateFile(name string, contents string) {
	err := ioutil.WriteFile(fix.path+"/"+name, []byte(contents), 0600)
	if err != nil {
		fix.t.Fatal(err)
	}
}

func (fix Fixture) RemoveFile(name string) {
	err := os.Remove(fix.path + "/" + name)
	if err != nil {
		fix.t.Fatal(err)
	}
}

func (fix Fixture) RenameFile(oldname string, newname string) {
	oldname = fix.path + "/" + oldname
	newname = fix.path + "/" + newname
	err := os.Rename(oldname, newname)
	if err != nil {
		fix.t.Fatal(err)
	}
}

func (fix Fixture) AppendFile(name string, contents string) {
	f, err := os.OpenFile(fix.path+"/"+name, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		fix.t.Fatal(err)
	}
	defer f.Close()
	_, err = f.WriteString(contents)
	if err != nil {
		fix.t.Fatal(err)
	}
}

func (fix Fixture) StartTail(name string, config Config) *Tail {
	tail, err := TailFile(fix.path+"/"+name, config)
	if err != nil {
		fix.t.Fatal(err)
	}
	return tail
}

func (fix Fixture) VerifyTail(tail *Tail, lines []string) {
	for idx, line := range lines {
		tailedLine, ok := <-tail.Lines
		if !ok {
			fix.t.Fatalf("tail ended early; expecting more: %v", lines[idx:])
		}
		if tailedLine == nil {
			fix.t.Fatalf("tail.Lines returned nil; not possible")
		}
		if tailedLine.Text != line {
			fix.t.Fatalf("mismatch; %s != %s", tailedLine.Text, line)
		}
	}
	line, ok := <-tail.Lines
	if ok {
		fix.t.Fatalf("more content from tail: %s", line.Text)
	}
}
